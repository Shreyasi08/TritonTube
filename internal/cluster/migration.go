package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/storage"
)

var ErrBusy = errors.New("another topology change is running")

// ErrCommitUncertain means the remote metadata transaction might have committed.
// Source data must be retained and future writes fenced until coordinator restart.
var ErrCommitUncertain = errors.New("topology commit outcome unknown; restart coordinator after metadata recovers")

type Migration struct {
	State         string     `json:"state"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Affected      int        `json:"affected"`
	Copied        int        `json:"copied"`
	Error         string     `json:"error,omitempty"`
	CleanupErrors []string   `json:"cleanup_errors,omitempty"`
}
type migrationState struct {
	mu    sync.RWMutex
	value Migration
}

func (r *Router) Migration() Migration {
	r.migration.mu.RLock()
	defer r.migration.mu.RUnlock()
	m := r.migration.value
	m.CleanupErrors = append([]string(nil), m.CleanupErrors...)
	return m
}
func (r *Router) setMigration(m Migration) {
	r.migration.mu.Lock()
	defer r.migration.mu.Unlock()
	r.migration.value = m
}

// RestoreMigration surfaces interrupted changes after a restart. Copying never
// commits topology; a committed record means destinations are already verified.
func (r *Router) RestoreMigration(m Migration) {
	if m.State == "copying" {
		m.State = "interrupted"
		m.Error = "process stopped before topology commit; old membership retained; destination duplicates may remain"
	}
	r.setMigration(m)
}
func (r *Router) Add(ctx context.Context, node Node) (Migration, error) {
	return r.change(ctx, func(old []Node) ([]Node, error) {
		for _, n := range old {
			if n.ID == node.ID {
				return nil, fmt.Errorf("node already exists")
			}
		}
		return append(old, node), nil
	})
}
func (r *Router) Remove(ctx context.Context, id string) (Migration, error) {
	return r.change(ctx, func(old []Node) ([]Node, error) {
		if len(old) <= 1 {
			return nil, fmt.Errorf("cannot remove last node")
		}
		next := make([]Node, 0, len(old)-1)
		found := false
		for _, n := range old {
			if n.ID == id {
				found = true
			} else {
				next = append(next, n)
			}
		}
		if !found {
			return nil, fmt.Errorf("node not found")
		}
		return next, nil
	})
}
func (r *Router) change(ctx context.Context, update func([]Node) ([]Node, error)) (m Migration, retErr error) {
	if !r.topology.TryLock() {
		return m, ErrBusy
	}
	defer r.topology.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uncertain {
		return r.Migration(), ErrCommitUncertain
	}
	if err := ctx.Err(); err != nil {
		return m, err
	}
	if r.inventory == nil {
		return m, fmt.Errorf("migration requires durable inventory")
	}
	nodes, err := update(append([]Node(nil), r.nodes...))
	if err != nil {
		return m, err
	}
	nextRing, nextClients, err := prepare(nodes, r.virtualNodes)
	if err != nil {
		return m, err
	}
	committed := false
	defer func() {
		if !committed {
			for _, c := range nextClients {
				c.Close()
			}
		}
	}()
	m = Migration{State: "copying", StartedAt: time.Now().UTC()}
	r.setMigration(m)
	defer func() {
		if retErr != nil {
			m.State = "failed"
			if errors.Is(retErr, ErrCommitUncertain) {
				m.State = "indeterminate"
				r.uncertain = true
			}
			m.Error = retErr.Error()
			finished := time.Now().UTC()
			m.FinishedAt = &finished
			r.setMigration(m)
			// Do not overwrite a possibly committed durable record with a guessed failure.
			if r.uncertain {
				return
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := r.inventory.SaveMigration(persistCtx, m); e != nil {
				retErr = errors.Join(retErr, fmt.Errorf("persist migration failure: %w", e))
			}
		}
	}()
	if err = r.inventory.SaveMigration(ctx, m); err != nil {
		return m, err
	}
	files, err := r.inventory.Files(ctx)
	if err != nil {
		return m, err
	}
	type move struct {
		file        File
		destination string
	}
	var moves []move
	updated := append([]File(nil), files...)
	for i, file := range files {
		oldOwner, e := r.ring.Owner(file.Key)
		if e != nil {
			return m, e
		}
		if oldOwner != file.Owner {
			return m, fmt.Errorf("ownership mismatch for %s", file.Key)
		}
		owner, e := nextRing.Owner(file.Key)
		if e != nil {
			return m, e
		}
		if owner != oldOwner {
			moves = append(moves, move{file, owner})
			updated[i].Owner = owner
		}
	}
	m.Affected = len(moves)
	r.setMigration(m)
	// A reachable destination is required even if this particular ring changes no files.
	for _, node := range nodes {
		healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Second)
		err = nextClients[node.ID].Health(healthCtx)
		healthCancel()
		if err != nil {
			return m, fmt.Errorf("node %s health: %w", node.ID, err)
		}
	}
	// A source and destination must not be aliases for the same physical data.
	// Otherwise post-commit source deletion would also delete the destination.
	identities := map[string]string{}
	checked := map[string]bool{}
	for _, group := range [][]Node{r.nodes, nodes} {
		for _, node := range group {
			if checked[node.ID] {
				continue
			}
			checked[node.ID] = true
			client := r.clients[node.ID]
			if client == nil {
				client = nextClients[node.ID]
			}
			identityCtx, identityCancel := context.WithTimeout(ctx, 2*time.Second)
			identity, e := client.Identity(identityCtx)
			identityCancel()
			if e != nil {
				return m, fmt.Errorf("node %s storage identity: %w", node.ID, e)
			}
			if prior, exists := identities[identity]; exists {
				return m, fmt.Errorf("nodes %s and %s share storage identity; distinct storage directories are required", prior, node.ID)
			}
			identities[identity] = node.ID
		}
	}
	for _, move := range moves {
		if err = copyVerified(ctx, r.clients[move.file.Owner], nextClients[move.destination], move.file); err != nil {
			return m, fmt.Errorf("migrate %s: %w", move.file.Key, err)
		}
		m.Copied++
		r.setMigration(m)
	}
	m.State = "committed"
	finished := time.Now().UTC()
	m.FinishedAt = &finished
	// The transaction persists membership, ownership and the committed status together.
	if err = r.inventory.CommitTopology(ctx, nodes, updated, m); err != nil {
		return m, fmt.Errorf("commit topology: %w", err)
	}
	oldClients := r.clients
	r.nodes = nodes
	r.ring = nextRing
	r.clients = nextClients
	committed = true
	r.setMigration(m)
	// Cleanup cannot invalidate the successful commit. Report retained duplicates explicitly.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()
	for _, move := range moves {
		err = oldClients[move.file.Owner].Delete(cleanupCtx, move.file.Key)
		if err != nil {
			m.CleanupErrors = append(m.CleanupErrors, fmt.Sprintf("%s on %s: %v", move.file.Key, move.file.Owner, err))
		}
	}
	for _, c := range oldClients {
		c.Close()
	}
	r.setMigration(m)
	if len(m.CleanupErrors) > 0 {
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = r.inventory.SaveMigration(persistCtx, m); err != nil {
			m.CleanupErrors = append(m.CleanupErrors, "persist cleanup warnings: "+err.Error())
			r.setMigration(m)
		}
	}
	return m, nil
}

type verifyWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *verifyWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("file larger than inventory")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func copyVerified(ctx context.Context, source, destination *storage.Client, file File) error {
	temp, err := os.CreateTemp("", "tritontube-migrate-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	hash := sha256.New()
	writer := &verifyWriter{writer: io.MultiWriter(temp, hash), remaining: file.Size}
	if err = source.Download(ctx, file.Key, writer); err != nil {
		return err
	}
	if writer.remaining != 0 || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("source checksum or size mismatch")
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	info, err := destination.Upload(ctx, file.Key, file.SHA256, temp)
	if err != nil {
		return err
	}
	if info.Sha256 != file.SHA256 || info.Size != file.Size {
		return fmt.Errorf("destination upload verification failed")
	}
	hash.Reset()
	writer = &verifyWriter{writer: hash, remaining: file.Size}
	if err = destination.Download(ctx, file.Key, writer); err != nil {
		return err
	}
	if writer.remaining != 0 || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("destination read-back checksum or size mismatch")
	}
	return nil
}

type NodeHealth struct {
	Node    Node   `json:"node"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

func (r *Router) Health(ctx context.Context) []NodeHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]NodeHealth, 0, len(r.nodes))
	for _, node := range r.nodes {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.clients[node.ID].Health(checkCtx)
		cancel()
		h := NodeHealth{Node: node, Healthy: err == nil}
		if err != nil {
			h.Error = err.Error()
		}
		result = append(result, h)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Node.ID < result[j].Node.ID })
	return result
}

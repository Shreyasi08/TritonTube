// Package etcdstore implements metadata.Repository using the official etcd v3
// client, with independent persistence logic and no SQLite fallback or custom consensus.
package etcdstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/hashring"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const MaxSnapshotBytes = 1 << 20
const operationTimeout = 5 * time.Second
const maxAttempts = 64

var ErrConflict = errors.New("metadata changed concurrently; retry operation")

type snapshot struct {
	Schema         int                       `json:"schema"`
	Videos         map[string]metadata.Video `json:"videos"`
	Files          map[string]cluster.File   `json:"files"`
	Nodes          []cluster.Node            `json:"nodes"`
	VirtualNodes   int                       `json:"virtual_nodes"`
	Migration      cluster.Migration         `json:"migration"`
	TopologyCommit string                    `json:"topology_commit,omitempty"`
}
type Store struct {
	client *clientv3.Client
	key    string
}

var _ metadata.Repository = (*Store)(nil)

// Open uses externally configured endpoints. Plain HTTP is for the local demo;
// production deployments must configure transport/authentication separately.
func Open(endpoints []string, prefix string) (*Store, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("at least one etcd endpoint required")
	}
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint) == "" {
			return nil, fmt.Errorf("empty etcd endpoint")
		}
	}
	if prefix == "" || prefix == "/" || !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != strings.TrimSuffix(prefix, "/") {
		return nil, fmt.Errorf("etcd prefix must be a non-root canonical absolute path")
	}
	client, err := clientv3.New(clientv3.Config{Endpoints: endpoints, DialTimeout: 3 * time.Second, DialKeepAliveTime: 2 * time.Second, DialKeepAliveTimeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	return &Store{client: client, key: strings.TrimSuffix(prefix, "/") + "/state"}, nil
}
func (s *Store) Close() error { return s.client.Close() }
func fresh() snapshot {
	return snapshot{Schema: 1, Videos: map[string]metadata.Video{}, Files: map[string]cluster.File{}, Migration: cluster.Migration{State: "idle"}}
}
func (s *Store) read(ctx context.Context) (snapshot, int64, error) {
	// No WithSerializable: a successful read requires a quorum-backed linearizable view.
	response, err := s.client.Get(ctx, s.key)
	if err != nil {
		return snapshot{}, 0, err
	}
	if len(response.Kvs) == 0 {
		return fresh(), 0, nil
	}
	var state snapshot
	if err = json.Unmarshal(response.Kvs[0].Value, &state); err != nil {
		return state, 0, fmt.Errorf("decode etcd metadata: %w", err)
	}
	if state.Schema != 1 || state.Videos == nil || state.Files == nil {
		return state, 0, fmt.Errorf("unsupported or invalid etcd metadata schema")
	}
	return state, response.Kvs[0].ModRevision, nil
}
func encode(state snapshot) ([]byte, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSnapshotBytes {
		return nil, fmt.Errorf("etcd metadata snapshot exceeds %d-byte demo limit", MaxSnapshotBytes)
	}
	return data, nil
}
func (s *Store) update(ctx context.Context, change func(*snapshot) error, topologyToken string) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		state, revision, err := s.read(ctx)
		if err != nil {
			return err
		}
		if err = change(&state); err != nil {
			return err
		}
		data, err := encode(state)
		if err != nil {
			return err
		}
		response, err := s.client.Txn(ctx).If(clientv3.Compare(clientv3.ModRevision(s.key), "=", revision)).Then(clientv3.OpPut(s.key, string(data))).Commit()
		if err != nil {
			if topologyToken != "" {
				// A lost response does not establish whether the transaction committed.
				// Reconcile with a fresh linearizable read; otherwise fence the coordinator.
				checkCtx, checkCancel := context.WithTimeout(context.Background(), operationTimeout)
				observed, _, readErr := s.read(checkCtx)
				checkCancel()
				if readErr == nil && observed.TopologyCommit == topologyToken {
					return nil
				}
				return fmt.Errorf("%w: etcd commit: %v; reconciliation: %v", cluster.ErrCommitUncertain, err, readErr)
			}
			return err
		}
		if response.Succeeded {
			return nil
		}
		// A failed comparison is known not to have committed. Retry from a new snapshot.
		timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrConflict
}
func (s *Store) view(ctx context.Context) (snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	state, _, err := s.read(ctx)
	return state, err
}
func (s *Store) Ping(ctx context.Context) error { _, err := s.view(ctx); return err }
func (s *Store) Create(ctx context.Context, video metadata.Video) error {
	return s.update(ctx, func(state *snapshot) error {
		if _, exists := state.Videos[video.ID]; exists {
			return fmt.Errorf("video already exists")
		}
		state.Videos[video.ID] = video
		return nil
	}, "")
}
func (s *Store) Get(ctx context.Context, id string) (metadata.Video, error) {
	state, err := s.view(ctx)
	if err != nil {
		return metadata.Video{}, err
	}
	video, exists := state.Videos[id]
	if !exists {
		return video, metadata.ErrNotFound
	}
	return video, nil
}
func (s *Store) List(ctx context.Context) ([]metadata.Video, error) {
	state, err := s.view(ctx)
	if err != nil {
		return nil, err
	}
	videos := make([]metadata.Video, 0, len(state.Videos))
	for _, video := range state.Videos {
		videos = append(videos, video)
	}
	sort.Slice(videos, func(i, j int) bool {
		if videos[i].CreatedAt.Equal(videos[j].CreatedAt) {
			return videos[i].ID < videos[j].ID
		}
		return videos[i].CreatedAt.After(videos[j].CreatedAt)
	})
	return videos, nil
}
func (s *Store) SetStatus(ctx context.Context, id, status, message string) error {
	return s.update(ctx, func(state *snapshot) error {
		video, exists := state.Videos[id]
		if !exists {
			return metadata.ErrNotFound
		}
		video.Status = status
		video.Error = message
		state.Videos[id] = video
		return nil
	}, "")
}
func (s *Store) PutFile(ctx context.Context, file cluster.File) error {
	return s.update(ctx, func(state *snapshot) error { state.Files[file.Key] = file; return nil }, "")
}
func (s *Store) File(ctx context.Context, key string) (cluster.File, error) {
	state, err := s.view(ctx)
	if err != nil {
		return cluster.File{}, err
	}
	file, exists := state.Files[key]
	if !exists {
		return file, metadata.ErrNotFound
	}
	return file, nil
}
func (s *Store) Files(ctx context.Context) ([]cluster.File, error) {
	state, err := s.view(ctx)
	if err != nil {
		return nil, err
	}
	files := make([]cluster.File, 0, len(state.Files))
	for _, file := range state.Files {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Key < files[j].Key })
	return files, nil
}
func (s *Store) Nodes(ctx context.Context) ([]cluster.Node, error) {
	state, err := s.view(ctx)
	if err != nil {
		return nil, err
	}
	if len(state.Nodes) == 0 {
		return nil, metadata.ErrNotFound
	}
	return state.Nodes, nil
}
func (s *Store) InitializeNodes(ctx context.Context, nodes []cluster.Node, vnodes int) ([]cluster.Node, error) {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	if len(nodes) == 0 {
		return nil, hashring.ErrEmpty
	}
	if _, err := hashring.New(ids, vnodes); err != nil {
		return nil, err
	}
	err := s.update(ctx, func(state *snapshot) error {
		if len(state.Nodes) == 0 {
			state.Nodes = append([]cluster.Node(nil), nodes...)
			state.VirtualNodes = vnodes
		}
		if state.VirtualNodes != vnodes {
			return fmt.Errorf("virtual node count differs from persisted ring: %d", state.VirtualNodes)
		}
		return nil
	}, "")
	if err != nil {
		return nil, err
	}
	return s.Nodes(ctx)
}
func (s *Store) SaveMigration(ctx context.Context, migration cluster.Migration) error {
	return s.update(ctx, func(state *snapshot) error { state.Migration = migration; return nil }, "")
}
func (s *Store) Migration(ctx context.Context) (cluster.Migration, error) {
	state, err := s.view(ctx)
	return state.Migration, err
}
func (s *Store) CommitTopology(ctx context.Context, nodes []cluster.Node, files []cluster.File, migration cluster.Migration) error {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	return s.update(ctx, func(state *snapshot) error {
		if len(files) != len(state.Files) {
			return fmt.Errorf("inventory changed during topology commit")
		}
		ids := make([]string, 0, len(nodes))
		for _, node := range nodes {
			ids = append(ids, node.ID)
		}
		if len(nodes) == 0 {
			return hashring.ErrEmpty
		}
		ring, err := hashring.New(ids, state.VirtualNodes)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, file := range files {
			old, exists := state.Files[file.Key]
			if !exists || seen[file.Key] || old.Size != file.Size || old.SHA256 != file.SHA256 {
				return fmt.Errorf("inventory changed for %s", file.Key)
			}
			owner, err := ring.Owner(file.Key)
			if err != nil || owner != file.Owner {
				return fmt.Errorf("invalid proposed owner for %s", file.Key)
			}
			seen[file.Key] = true
		}
		for _, file := range files {
			state.Files[file.Key] = file
		}
		state.Nodes = append([]cluster.Node(nil), nodes...)
		state.Migration = migration
		state.TopologyCommit = token
		return nil
	}, token)
}
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	return s.update(ctx, func(state *snapshot) error {
		for id, video := range state.Videos {
			if video.Status == "uploading" || video.Status == "processing" {
				video.Status = "failed"
				video.Error = "server restarted during upload or processing; upload again"
				state.Videos[id] = video
			}
		}
		return nil
	}, "")
}

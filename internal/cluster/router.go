// Package cluster coordinates routing and graceful membership changes.
package cluster

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/Shreyasi08/TritonTube/internal/hashring"
	"github.com/Shreyasi08/TritonTube/internal/storage"
)

type Node struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}
type File struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Owner  string `json:"owner"`
}
type Inventory interface {
	PutFile(context.Context, File) error
	Files(context.Context) ([]File, error)
	CommitTopology(context.Context, []Node, []File, Migration) error
	SaveMigration(context.Context, Migration) error
}
type Router struct {
	topology     sync.Mutex
	migration    migrationState
	mu           sync.RWMutex
	nodes        []Node
	ring         *hashring.Ring
	clients      map[string]*storage.Client
	virtualNodes int
	inventory    Inventory
	uncertain    bool
}

func New(nodes []Node, virtualNodes int, inventory Inventory) (*Router, error) {
	ring, clients, err := prepare(nodes, virtualNodes)
	if err != nil {
		return nil, err
	}
	return &Router{nodes: append([]Node(nil), nodes...), ring: ring, clients: clients, virtualNodes: virtualNodes, inventory: inventory, migration: migrationState{value: Migration{State: "idle"}}}, nil
}
func prepare(nodes []Node, virtualNodes int) (*hashring.Ring, map[string]*storage.Client, error) {
	if len(nodes) == 0 {
		return nil, nil, hashring.ErrEmpty
	}
	ids := make([]string, 0, len(nodes))
	addresses := map[string]bool{}
	for _, node := range nodes {
		if node.Address == "" || addresses[node.Address] {
			return nil, nil, fmt.Errorf("empty or duplicate node address")
		}
		addresses[node.Address] = true
		ids = append(ids, node.ID)
	}
	ring, err := hashring.New(ids, virtualNodes)
	if err != nil {
		return nil, nil, err
	}
	clients := map[string]*storage.Client{}
	for _, node := range nodes {
		c, e := storage.Dial(node.Address)
		if e != nil {
			for _, c := range clients {
				c.Close()
			}
			return nil, nil, e
		}
		clients[node.ID] = c
	}
	return ring, clients, nil
}
func (r *Router) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.clients {
		c.Close()
	}
}
func (r *Router) Nodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	nodes := append([]Node(nil), r.nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}
func (r *Router) Upload(ctx context.Context, key, checksum string, reader io.Reader) (File, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.uncertain {
		return File{}, ErrCommitUncertain
	}
	owner, err := r.ring.Owner(key)
	if err != nil {
		return File{}, err
	}
	info, err := r.clients[owner].Upload(ctx, key, checksum, reader)
	if err != nil {
		return File{}, err
	}
	file := File{Key: key, Size: info.Size, SHA256: info.Sha256, Owner: owner}
	if r.inventory != nil {
		if err = r.inventory.PutFile(ctx, file); err != nil {
			return File{}, fmt.Errorf("record stored file: %w", err)
		}
	}
	return file, nil
}
func (r *Router) Download(ctx context.Context, key string, w io.Writer) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner, err := r.ring.Owner(key)
	if err != nil {
		return err
	}
	return r.clients[owner].Download(ctx, key, w)
}

func (r *Router) Ready() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.uncertain {
		return ErrCommitUncertain
	}
	return nil
}

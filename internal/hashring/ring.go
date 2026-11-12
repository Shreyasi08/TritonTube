// Package hashring provides an immutable, deterministic SHA-256 consistent hash ring.
package hashring

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const DefaultVirtualNodes = 100

var ErrEmpty = errors.New("no storage nodes")

type point struct {
	position uint64
	node     string
	replica  int
}
type Ring struct {
	points []point
	hash   func(string) uint64
}

func Hash(key string) uint64 {
	sum := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint64(sum[:8])
}
func New(nodes []string, virtualNodes int) (*Ring, error) { return newRing(nodes, virtualNodes, Hash) }
func newRing(nodes []string, virtualNodes int, hash func(string) uint64) (*Ring, error) {
	if virtualNodes < 1 || virtualNodes > 10000 {
		return nil, fmt.Errorf("virtual nodes must be between 1 and 10000")
	}
	r := &Ring{hash: hash}
	seen := map[string]bool{}
	for _, node := range nodes {
		if node == "" || seen[node] {
			return nil, fmt.Errorf("empty or duplicate node identity %q", node)
		}
		seen[node] = true
		for i := 0; i < virtualNodes; i++ {
			key := fmt.Sprintf("node:%d:%s:%d", len(node), node, i)
			r.points = append(r.points, point{hash(key), node, i})
		}
	}
	// Collisions are ordered by node identity, then virtual-node index. The first
	// point at a position owns that boundary, independent of insertion order.
	sort.Slice(r.points, func(i, j int) bool {
		a, b := r.points[i], r.points[j]
		if a.position != b.position {
			return a.position < b.position
		}
		if a.node != b.node {
			return a.node < b.node
		}
		return a.replica < b.replica
	})
	return r, nil
}
func (r *Ring) Owner(key string) (string, error) { return r.ownerAt(r.hash(key)) }
func (r *Ring) ownerAt(position uint64) (string, error) {
	if len(r.points) == 0 {
		return "", ErrEmpty
	}
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].position >= position })
	if i == len(r.points) {
		i = 0
	}
	return r.points[i].node, nil
}

// Package metadata defines backend-independent video and inventory persistence.
package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
)

var ErrNotFound = errors.New("metadata record not found")

type Video struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
}
type Repository interface {
	cluster.Inventory
	File(context.Context, string) (cluster.File, error)
	Create(context.Context, Video) error
	Get(context.Context, string) (Video, error)
	List(context.Context) ([]Video, error)
	SetStatus(context.Context, string, string, string) error
	Nodes(context.Context) ([]cluster.Node, error)
	InitializeNodes(context.Context, []cluster.Node, int) ([]cluster.Node, error)
	Migration(context.Context) (cluster.Migration, error)
	RecoverInterrupted(context.Context) error
	Ping(context.Context) error
	Close() error
}

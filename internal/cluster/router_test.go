package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/storage"
	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc"
)

func startNode(t *testing.T, id string) Node {
	t.Helper()
	store, err := storage.NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterStorageServer(server, store)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close(); store.Close() })
	return Node{ID: id, Address: listener.Addr().String()}
}
func TestMultiNodeRouting(t *testing.T) {
	nodes := []Node{startNode(t, "a"), startNode(t, "b"), startNode(t, "c")}
	router, err := New(nodes, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owners := map[string]bool{}
	for i := 0; i < 60; i++ {
		key := fmt.Sprintf("video/segment-%d.m4s", i)
		data := bytes.Repeat([]byte(key), 5000)
		checksum := fmt.Sprintf("%x", sha256.Sum256(data))
		file, err := router.Upload(ctx, key, checksum, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		owners[file.Owner] = true
		var got bytes.Buffer
		if err = router.Download(ctx, key, &got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, got.Bytes()) {
			t.Fatal("corrupt routed download")
		}
	}
	if len(owners) != 3 {
		t.Fatal("not distributed", owners)
	}
}

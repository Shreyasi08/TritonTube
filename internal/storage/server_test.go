package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testNode(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewServer(dir)
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
	client, err := Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Stop(); listener.Close(); store.Close() })
	return client, dir
}
func TestStreamingLifecycle(t *testing.T) {
	c, dir := testNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data := bytes.Repeat([]byte("video-segment"), 100000)
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	info, err := c.Upload(ctx, "video/segment.m4s", sum, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) || info.Sha256 != sum {
		t.Fatal(info)
	}
	var got bytes.Buffer
	if err = c.Download(ctx, info.Key, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, got.Bytes()) {
		t.Fatal("corrupt download")
	}
	files, err := c.List(ctx)
	if err != nil || len(files) != 1 {
		t.Fatalf("list: %v %v", files, err)
	}
	if _, err = c.Upload(ctx, "video/segment.m4s", fmt.Sprintf("%064d", 0), bytes.NewReader([]byte("bad"))); status.Code(err) != codes.DataLoss {
		t.Fatalf("checksum: %v", err)
	}
	got.Reset()
	if err = c.Download(ctx, info.Key, &got); err != nil || !bytes.Equal(data, got.Bytes()) {
		t.Fatal("failed upload replaced original", err)
	}
	staging, err := os.ReadDir(filepath.Join(dir, ".staging"))
	if err != nil || len(staging) != 0 {
		t.Fatal("temporary files leaked", err)
	}
	if err = c.Delete(ctx, info.Key); err != nil {
		t.Fatal(err)
	}
	if err = c.Download(ctx, info.Key, &got); status.Code(err) != codes.NotFound {
		t.Fatalf("missing: %v", err)
	}
}
func TestInvalidKeysAndSymlinkEscape(t *testing.T) {
	c, dir := testNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sum := fmt.Sprintf("%x", sha256.Sum256(nil))
	for _, key := range []string{"", "../escape", "/absolute", "a/../b", "a//b", "a\\b", ".staging/x"} {
		if _, err := c.Upload(ctx, key, sum, bytes.NewReader(nil)); err == nil {
			t.Errorf("accepted %q", key)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(ctx, "escape/file", sum, bytes.NewReader(nil)); err == nil {
		t.Fatal("escaped root")
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Fatal("wrote outside root")
	}
}
func TestCancellationDoesNotPublish(t *testing.T) {
	c, dir := testNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Upload(ctx, "video/file", fmt.Sprintf("%064d", 0), bytes.NewReader(nil)); err == nil {
		t.Fatal("canceled upload succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "video/file")); !os.IsNotExist(err) {
		t.Fatal("published canceled upload")
	}
}

// A destination may violate io.Writer's contract by returning a short write
// without an error. The client must not report a truncated download as success.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestDownloadRejectsShortWrites(t *testing.T) {
	c, _ := testNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data := bytes.Repeat([]byte("segment"), 20000)
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := c.Upload(ctx, "video/short-write.m4s", sum, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(ctx, "video/short-write.m4s", shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("expected short-write error, got %v", err)
	}
}
func TestUploadPreservesEarlyServerError(t *testing.T) {
	c, _ := testNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data := bytes.Repeat([]byte("rejected"), 1<<19)
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := c.Upload(ctx, "../escape", sum, bytes.NewReader(data)); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected server InvalidArgument, got %v", err)
	}
}
func TestUploadRejectsInvalidStreamMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message *pb.UploadRequest
	}{
		{"duplicate header", &pb.UploadRequest{Payload: &pb.UploadRequest_Header{Header: &pb.FileHeader{Key: "video/file", Sha256: fmt.Sprintf("%x", sha256.Sum256(nil))}}}},
		{"oversized chunk", &pb.UploadRequest{Payload: &pb.UploadRequest_Chunk{Chunk: make([]byte, ChunkSize+1)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, dir := testNode(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stream, err := c.rpc.Upload(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = stream.Send(&pb.UploadRequest{Payload: &pb.UploadRequest_Header{Header: &pb.FileHeader{Key: "video/file", Sha256: fmt.Sprintf("%x", sha256.Sum256(nil))}}}); err != nil {
				t.Fatal(err)
			}
			if err = stream.Send(tc.message); err != nil {
				t.Fatal(err)
			}
			if _, err = stream.CloseAndRecv(); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("expected invalid stream error: %v", err)
			}
			if _, err = os.Stat(filepath.Join(dir, "video/file")); !os.IsNotExist(err) {
				t.Fatal("published invalid upload", err)
			}
			entries, err := os.ReadDir(filepath.Join(dir, ".staging"))
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid upload leaked temporary file", err)
			}
		})
	}
}

func TestPersistentStorageIdentity(t *testing.T) {
	dir := t.TempDir()
	first, err := NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := first.identity
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.identity != id {
		t.Fatal("restart changed storage identity")
	}
	other, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if other.identity == id {
		t.Fatal("independent storage directories share identity")
	}
	client, _ := testNode(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remote, err := client.Identity(ctx)
	if err != nil || len(remote) != 32 {
		t.Fatal(remote, err)
	}
	files, err := client.List(ctx)
	if err != nil || len(files) != 0 {
		t.Fatal("identity leaked into media inventory", files, err)
	}
}

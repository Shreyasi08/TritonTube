package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/api"
	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/hashring"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
	"github.com/Shreyasi08/TritonTube/internal/storage"
	"github.com/Shreyasi08/TritonTube/internal/transcode"
	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Choose an identity that moves at least one media file, independent of the
// randomly assigned video ID. This guarantees that playback exercises migration.
func joiningID(t *testing.T, nodes []cluster.Node, files []cluster.File) string {
	t.Helper()
	ids := make([]string, 0, len(nodes)+1)
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("node4-%d", i)
		ring, err := hashring.New(append(ids, candidate), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file.Key, "source.mp4") {
				continue
			}
			owner, err := ring.Owner(file.Key)
			if err != nil {
				t.Fatal(err)
			}
			if owner == candidate && file.Owner != candidate {
				return candidate
			}
		}
	}
	t.Fatal("could not find a joining identity affecting media")
	return ""
}
func decodeDASH(t *testing.T, base, id, stage string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", base+"/stream/"+id+"/manifest.mpd", "-map", "0:v:0", "-map", "0:v:1", "-map", "0:a?", "-t", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("%s DASH playback: %v %s", stage, err, output)
	}
	t.Logf("%s: HTTP DASH decoded both video representations and audio", stage)
}
func verifyHTTPInventory(t *testing.T, f *fixture, base string) {
	t.Helper()
	files, err := f.repo.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		resp, err := http.Get(base + "/stream/" + file.Key)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		size, err := io.Copy(hash, resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || size != file.Size || fmt.Sprintf("%x", hash.Sum(nil)) != file.SHA256 {
			t.Fatalf("HTTP inventory mismatch %s: status=%d size=%d error=%v", file.Key, resp.StatusCode, size, err)
		}
	}
}
func TestAdminNodeChangesKeepDASHPlayable(t *testing.T) {
	source := generateVideo(t, true)
	f := setup(t, func(r *cluster.Router) api.Processor { return &transcode.FFmpeg{Router: r} })
	code, body := upload(t, f.http.URL, source, "sample.mp4")
	if code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	var video metadata.Video
	if err := json.Unmarshal(body, &video); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cli := filepath.Join(t.TempDir(), "admin")
	if output, err := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", cli, "../cmd/admin").CombinedOutput(); err != nil {
		t.Fatalf("build admin CLI: %v %s", err, output)
	}
	runCLI := func(args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, cli, append([]string{"-url", f.http.URL}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("admin %v: %v %s", args, err, output)
		}
		return output
	}
	var initial []cluster.Node
	if err := json.Unmarshal(runCLI("list"), &initial); err != nil || len(initial) != 3 {
		t.Fatal("initial list", initial, err)
	}
	before, err := f.repo.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := joiningID(t, f.nodes, before)
	fourth, stopFourth := storageNode(t, id)
	f.stops[id] = stopFourth
	decodeDASH(t, f.http.URL, video.ID, "before node changes")
	var added cluster.Migration
	if err = json.Unmarshal(runCLI("add", fourth.ID, fourth.Address), &added); err != nil {
		t.Fatal(err)
	}
	if added.State != "committed" || added.Affected == 0 || added.Affected != added.Copied || len(added.CleanupErrors) > 0 {
		t.Fatal("add migration", added)
	}
	newNodes := append(append([]cluster.Node(nil), f.nodes...), fourth)
	ids := make([]string, 0, len(newNodes))
	for _, n := range newNodes {
		ids = append(ids, n.ID)
	}
	ring, err := hashring.New(ids, 100)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.repo.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range after {
		owner, e := ring.Owner(file.Key)
		if e != nil || owner != file.Owner {
			t.Fatal("owner differs from new ring", file, e)
		}
	}
	verifyHTTPInventory(t, f, f.http.URL)
	decodeDASH(t, f.http.URL, video.ID, "after CLI add")
	t.Logf("CLI add: %d files copied and verified before membership commit", added.Copied)
	// Prefer an original node with media to ensure a useful graceful departure.
	removeID := fourth.ID
	for _, file := range after {
		if file.Owner != fourth.ID && !strings.HasSuffix(file.Key, "source.mp4") {
			removeID = file.Owner
			break
		}
	}
	var removed cluster.Migration
	if err = json.Unmarshal(runCLI("remove", removeID), &removed); err != nil {
		t.Fatal(err)
	}
	if removed.State != "committed" || removed.Affected == 0 || removed.Affected != removed.Copied || len(removed.CleanupErrors) > 0 {
		t.Fatal("remove migration", removed)
	}
	f.stops[removeID]()
	verifyHTTPInventory(t, f, f.http.URL)
	decodeDASH(t, f.http.URL, video.ID, "after CLI remove and node shutdown")
	t.Logf("CLI remove %s: %d files retained on verified destinations", removeID, removed.Copied)
	var listed []cluster.Node
	if err = json.Unmarshal(runCLI("list"), &listed); err != nil || len(listed) != 3 {
		t.Fatal("final list", listed, err)
	}
	for _, node := range listed {
		if node.ID == removeID {
			t.Fatal("removed node still listed")
		}
	}
	runCLI("health")
	var status cluster.Migration
	if err = json.Unmarshal(runCLI("migration"), &status); err != nil || status.State != "committed" {
		t.Fatal(status, err)
	}
	// Recreate the coordinator from the SQLite-committed membership, with the
	// departed process still stopped. No fallback to the original seed list.
	persisted, err := f.repo.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := cluster.New(persisted, 100, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	server := httptest.NewServer(api.New(f.repo, restarted, f.work, &transcode.FFmpeg{Router: restarted}).Handler())
	defer server.Close()
	decodeDASH(t, server.URL, video.ID, "after coordinator reconstruction")
}

// Fault injection delegates upload to real storage but corrupts download bytes,
// so the migration must detect a dishonest destination's read-back response.
type corruptReadStorage struct{ pb.StorageServer }
type corruptReadStream struct{ pb.Storage_DownloadServer }

func (s corruptReadStream) Send(message *pb.DownloadResponse) error {
	data := append([]byte(nil), message.Chunk...)
	if len(data) > 0 {
		data[0] ^= 0xff
	}
	return s.Storage_DownloadServer.Send(&pb.DownloadResponse{Chunk: data})
}
func (s corruptReadStorage) Download(req *pb.FileRequest, stream pb.Storage_DownloadServer) error {
	return s.StorageServer.Download(req, corruptReadStream{stream})
}
func corruptDestination(t *testing.T, id string) cluster.Node {
	t.Helper()
	store, err := storage.NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterStorageServer(server, corruptReadStorage{store})
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, hs)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close(); store.Close() })
	return cluster.Node{ID: id, Address: listener.Addr().String()}
}
func TestMigrationRejectsCorruptDestinationReadback(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	before, err := f.repo.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node := corruptDestination(t, joiningID(t, f.nodes, before))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := f.router.Add(ctx, node)
	if err == nil || result.State != "failed" || !strings.Contains(err.Error(), "destination read-back checksum") {
		t.Fatal("corruption not rejected", result, err)
	}
	if len(f.router.Nodes()) != 3 {
		t.Fatal("corrupt destination committed")
	}
	verifyFiles(t, f.router, contents)
	after, err := f.repo.Files(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("inventory changed after checksum failure", err)
	}
	nodes, err := f.repo.Nodes(ctx)
	if err != nil || len(nodes) != 3 {
		t.Fatal("membership changed after checksum failure", nodes, err)
	}
}

func TestMigrationRejectsCorruptSource(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	files, err := f.repo.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	node, _ := storageNode(t, joiningID(t, f.nodes, files))
	ids := []string{"node1", "node2", "node3", node.ID}
	ring, err := hashring.New(ids, 100)
	if err != nil {
		t.Fatal(err)
	}
	var target cluster.File
	for _, file := range files {
		owner, _ := ring.Owner(file.Key)
		if owner != file.Owner {
			target = file
			break
		}
	}
	var address string
	for _, n := range f.nodes {
		if n.ID == target.Owner {
			address = n.Address
		}
	}
	client, err := storage.Dial(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	corrupt := append([]byte(nil), contents[target.Key]...)
	corrupt[0] ^= 0xff
	sum := fmt.Sprintf("%x", sha256.Sum256(corrupt))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err = client.Upload(ctx, target.Key, sum, bytes.NewReader(corrupt)); err != nil {
		t.Fatal(err)
	}
	result, err := f.router.Add(ctx, node)
	if err == nil || result.State != "failed" || !strings.Contains(err.Error(), "source checksum") {
		t.Fatal("source corruption not rejected", result, err)
	}
	// The test intentionally corrupted this source before migration. All other
	// good source copies must remain available, and migration must not delete it.
	contents[target.Key] = corrupt
	verifyFiles(t, f.router, contents)
	after, err := f.repo.Files(ctx)
	if err != nil || !reflect.DeepEqual(files, after) {
		t.Fatal("source failure changed inventory", err)
	}
}

func TestMigrationRejectsAddressAlias(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	alias := cluster.Node{ID: "alias", Address: strings.Replace(f.nodes[0].Address, "127.0.0.1", "localhost", 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := f.router.Add(ctx, alias)
	if err == nil || result.State != "failed" || !strings.Contains(err.Error(), "storage identity") {
		t.Fatal("same storage accepted as an independent node", result, err)
	}
	if len(f.router.Nodes()) != 3 {
		t.Fatal("alias committed")
	}
	verifyFiles(t, f.router, contents)
}

type loseCommitResponse struct{ metadata.Repository }

func (r loseCommitResponse) CommitTopology(ctx context.Context, nodes []cluster.Node, files []cluster.File, migration cluster.Migration) error {
	if err := r.Repository.CommitTopology(ctx, nodes, files, migration); err != nil {
		return err
	}
	return cluster.ErrCommitUncertain
}
func TestUnknownCommitRetainsCopiesAndFencesWrites(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	router, err := cluster.New(f.nodes, 100, loseCommitResponse{f.repo})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	fourth, _ := storageNode(t, "node4")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := router.Add(ctx, fourth)
	if !errors.Is(err, cluster.ErrCommitUncertain) || result.State != "indeterminate" {
		t.Fatal(result, err)
	}
	verifyFiles(t, router, contents)
	if !errors.Is(router.Ready(), cluster.ErrCommitUncertain) {
		t.Fatal("uncertain coordinator still ready")
	}
	if _, err = router.Upload(ctx, "new/file", "", bytes.NewReader(nil)); !errors.Is(err, cluster.ErrCommitUncertain) {
		t.Fatal("uncertain coordinator allowed writes", err)
	}
	if _, err = router.Remove(ctx, "node1"); !errors.Is(err, cluster.ErrCommitUncertain) {
		t.Fatal("uncertain coordinator allowed topology changes", err)
	}
	persisted, err := f.repo.Migration(ctx)
	if err != nil || persisted.State != "committed" {
		t.Fatal("overwrote actual durable commit", persisted, err)
	}
	nodes, err := f.repo.Nodes(ctx)
	if err != nil || len(nodes) != 4 {
		t.Fatal(nodes, err)
	}
	recovered, err := cluster.New(nodes, 100, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	verifyFiles(t, recovered, contents)
}

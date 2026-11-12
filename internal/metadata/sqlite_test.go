package metadata

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
)

func TestSQLitePersistenceAndAtomicTopology(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "metadata.db")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	nodes := []cluster.Node{{ID: "a", Address: "localhost:9001"}}
	if _, err = s.InitializeNodes(ctx, nodes, 100); err != nil {
		t.Fatal(err)
	}
	v := Video{ID: "video", Title: "Example", Status: "uploading", CreatedAt: time.Now().UTC(), Size: 123}
	if err = s.Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	file := cluster.File{Key: "video/source.mp4", SHA256: "checksum", Size: 123, Owner: "a"}
	if err = s.PutFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err = s.SetStatus(ctx, "video", "ready", ""); err != nil {
		t.Fatal(err)
	}
	bad := file
	bad.Size++
	bad.Owner = "b"
	if err = s.CommitTopology(ctx, []cluster.Node{{ID: "b", Address: "localhost:9002"}}, []cluster.File{bad}, cluster.Migration{State: "committed"}); err == nil {
		t.Fatal("accepted stale inventory")
	}
	gotNodes, err := s.Nodes(ctx)
	if err != nil || gotNodes[0].ID != "a" {
		t.Fatal("failed commit changed membership", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(ctx, "video")
	if err != nil || got.Status != "ready" || got.Title != v.Title {
		t.Fatal(got, err)
	}
	if _, err = s.InitializeNodes(ctx, nodes, 101); err == nil {
		t.Fatal("allowed ring configuration drift")
	}
	files, err := s.Files(ctx)
	if err != nil || len(files) != 1 || files[0].Owner != "a" {
		t.Fatal(files, err)
	}
}

func TestRecoverInterruptedUploads(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	for _, state := range []string{"uploading", "processing", "ready", "failed"} {
		if err = repo.Create(ctx, Video{ID: state, Title: state, Status: state, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"uploading", "processing", "ready", "failed"} {
		v, e := repo.Get(ctx, state)
		if e != nil {
			t.Fatal(e)
		}
		want := state
		if state == "uploading" || state == "processing" {
			want = "failed"
			if v.Error == "" {
				t.Fatal("missing interruption diagnostic")
			}
		}
		if v.Status != want {
			t.Fatalf("%s: %s", state, v.Status)
		}
	}
}

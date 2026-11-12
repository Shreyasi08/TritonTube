package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/hashring"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
	"github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcdThreeMemberFailover(t *testing.T) {
	if os.Getenv("TRITONTUBE_ETCD_TEST") != "1" {
		t.Skip("run make test-etcd to exercise a real three-member etcd cluster")
	}
	for _, tool := range []string{"docker", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required etcd test tool %s: %v", tool, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	output, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("Docker unavailable: %v %s", err, output)
	}
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	project := "tritontube-etcd-test-" + hex.EncodeToString(random)
	image := project + ":test"
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TRITONTUBE_IMAGE="+image, "TRITONTUBE_HTTP_PORT=0", "TRITONTUBE_ETCD1_PORT=0", "TRITONTUBE_ETCD2_PORT=0", "TRITONTUBE_ETCD3_PORT=0")
	compose := func(ctx context.Context, args ...string) ([]byte, error) {
		prefix := []string{"compose", "--project-name", project, "--file", filepath.Join(root, "docker-compose.yml"), "--file", filepath.Join(root, "docker-compose.etcd.yml"), "--profile", "etcd"}
		command := exec.CommandContext(ctx, "docker", append(prefix, args...)...)
		command.Env = env
		return command.CombinedOutput()
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if t.Failed() {
			logs, _ := compose(cleanupCtx, "logs", "--no-color", "--tail", "60")
			t.Logf("etcd diagnostics:\n%s", logs)
		}
		output, e := compose(cleanupCtx, "--profile", "scale", "down", "--volumes", "--remove-orphans")
		if e != nil {
			t.Errorf("cleanup %s: %v %s", project, e, output)
		}
		// Remove our unique application image only; preserve the shared official etcd image.
		if exec.CommandContext(cleanupCtx, "docker", "image", "inspect", image).Run() == nil {
			if output, e = exec.CommandContext(cleanupCtx, "docker", "image", "rm", image).CombinedOutput(); e != nil {
				t.Errorf("cleanup image: %v %s", e, output)
			}
		}
	}()
	run := func(args ...string) []byte {
		t.Helper()
		opCtx, opCancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer opCancel()
		output, e := compose(opCtx, args...)
		if e != nil {
			t.Fatalf("compose %v: %v\n%s", args, e, output)
		}
		return output
	}
	t.Logf("isolated etcd project: %s", project)
	run("up", "--build", "-d", "--wait", "--wait-timeout", "120", "web")
	endpoints := make([]string, 3)
	for i := range endpoints {
		endpoints[i] = "http://" + strings.TrimSpace(string(run("port", fmt.Sprintf("etcd%d", i+1), "2379")))
	}
	base := "http://" + strings.TrimSpace(string(run("port", "web", "8080")))
	store, err := etcdstore.Open(endpoints, "/contracts")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := etcdstore.Open(endpoints, "/contracts")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	exerciseEtcdRepository(t, store, other)
	// Actual web process uses etcd metadata; SQLite's --db flag is not involved.
	source := generateVideo(t, true)
	code, body := upload(t, base, source, "before-failure.mp4")
	if code != 201 {
		t.Fatalf("etcd upload: %d %s", code, body)
	}
	var video metadata.Video
	if err = json.Unmarshal(body, &video); err != nil || video.Status != "ready" {
		t.Fatal(video, err)
	}
	appStore, err := etcdstore.Open(endpoints, "/tritontube")
	if err != nil {
		t.Fatal(err)
	}
	defer appStore.Close()
	before, err := appStore.Get(context.Background(), video.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeFiles, err := appStore.Files(context.Background())
	if err != nil || len(beforeFiles) < 3 {
		t.Fatal(beforeFiles, err)
	}
	beforeNodes, err := appStore.Nodes(context.Background())
	if err != nil || len(beforeNodes) != 3 {
		t.Fatal(beforeNodes, err)
	}
	decodeDASH(t, base, video.ID, "etcd before member failure")
	native, err := clientv3.New(clientv3.Config{Endpoints: endpoints, DialTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	leader := -1
	for i, endpoint := range endpoints {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
		status, e := native.Status(statusCtx, endpoint)
		statusCancel()
		if e != nil {
			t.Fatal(e)
		}
		if status.Header.MemberId == status.Leader {
			leader = i
		}
	}
	if leader < 0 {
		t.Fatal("could not identify the real etcd leader")
	}
	failedService := fmt.Sprintf("etcd%d", leader+1)
	run("kill", "--signal", "SIGKILL", failedService)
	t.Logf("abruptly killed leader %s; two of three etcd members remain", failedService)
	deadline := time.Now().Add(30 * time.Second)
	for {
		healthCtx, healthCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = appStore.Ping(healthCtx)
		healthCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("metadata quorum did not recover: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	after, err := appStore.Get(context.Background(), video.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("video metadata continuity failed", after, err)
	}
	files, err := appStore.Files(context.Background())
	if err != nil || !reflect.DeepEqual(beforeFiles, files) {
		t.Fatal("inventory continuity failed", err)
	}
	nodes, err := appStore.Nodes(context.Background())
	if err != nil || !reflect.DeepEqual(beforeNodes, nodes) {
		t.Fatal("membership continuity failed", err)
	}
	// Write with one member down, and read through a separate client restricted to survivors.
	var surviving []string
	for i, endpoint := range endpoints {
		if i != leader {
			surviving = append(surviving, endpoint)
		}
	}
	survivor, err := etcdstore.Open(surviving, "/contracts")
	if err != nil {
		t.Fatal(err)
	}
	defer survivor.Close()
	record := metadata.Video{ID: "after-failure", Title: "Quorum write", Status: "ready", CreatedAt: time.Now().UTC()}
	if err = store.Create(context.Background(), record); err != nil {
		t.Fatal("post-failure metadata write", err)
	}
	got, err := survivor.Get(context.Background(), record.ID)
	if err != nil || got.Title != record.Title {
		t.Fatal("post-failure read via survivors", got, err)
	}
	if err = survivor.SetStatus(context.Background(), record.ID, "failed", "continuity check"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(context.Background(), record.ID)
	if err != nil || got.Error != "continuity check" {
		t.Fatal(got, err)
	}
	// Wait for the web client's election/endpoint transition, not just the test client.
	client := &http.Client{Timeout: 3 * time.Second}
	deadline = time.Now().Add(30 * time.Second)
	for {
		resp, e := client.Get(base + "/healthz")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("web did not regain metadata health")
		}
		time.Sleep(100 * time.Millisecond)
	}
	decodeDASH(t, base, video.ID, "etcd after one member failure (all media nodes still running)")
	code, body = upload(t, base, source, "after-failure.mp4")
	if code != 201 {
		t.Fatalf("new video after one etcd failure: %d %s", code, body)
	}
	var second metadata.Video
	if err = json.Unmarshal(body, &second); err != nil || second.Status != "ready" {
		t.Fatal(second, err)
	}
	decodeDASH(t, base, second.ID, "new video uploaded with two-member etcd quorum")
	// Also exercise the actual routing-commit path through etcd with one member down.
	run("--profile", "scale", "up", "-d", "--no-build", "--wait", "--wait-timeout", "60", "storage4")
	allFiles, err := appStore.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joining := joiningID(t, beforeNodes, allFiles)
	var migrated cluster.Migration
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "add", joining, "storage4:9001"), &migrated); err != nil || migrated.State != "committed" || migrated.Copied == 0 || len(migrated.CleanupErrors) > 0 {
		t.Fatal("etcd-backed node join after leader failure", migrated, err)
	}
	persistedNodes, err := appStore.Nodes(context.Background())
	if err != nil || len(persistedNodes) != 4 {
		t.Fatal("etcd topology transaction", persistedNodes, err)
	}
	decodeDASH(t, base, second.ID, "etcd-backed storage join while one etcd member is down")
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "remove", joining), &migrated); err != nil || migrated.State != "committed" || len(migrated.CleanupErrors) > 0 {
		t.Fatal("etcd-backed node removal", migrated, err)
	}
	run("--profile", "scale", "stop", "storage4")
	decodeDASH(t, base, second.ID, "etcd-backed graceful storage removal (no media crash simulated)")
	t.Logf("metadata video, inventory, membership, concurrent CAS updates, new uploads and topology commits continued after leader failure")
	// Losing the majority must return errors rather than stale cached metadata.
	secondFailure := (leader + 1) % 3
	run("kill", "--signal", "SIGKILL", fmt.Sprintf("etcd%d", secondFailure+1))
	noQuorumCtx, noQuorumCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	err = appStore.Ping(noQuorumCtx)
	noQuorumCancel()
	if err == nil {
		t.Fatal("linearizable metadata read succeeded without quorum")
	}
	noQuorumCtx, noQuorumCancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
	err = store.Create(noQuorumCtx, metadata.Video{ID: "without-quorum", CreatedAt: time.Now().UTC()})
	noQuorumCancel()
	if err == nil {
		t.Fatal("metadata write succeeded without quorum")
	}
	t.Log("two failed members: quorum reads/writes correctly failed; no unplanned media-node recovery or replication claimed")
}

func exerciseEtcdRepository(t *testing.T, first, second *etcdstore.Store) {
	t.Helper()
	ctx := context.Background()
	initial := []cluster.Node{{ID: "a", Address: "unused:1"}}
	if _, err := first.InitializeNodes(ctx, initial, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := second.InitializeNodes(ctx, initial, 101); err == nil {
		t.Fatal("ring configuration changed")
	}
	for _, state := range []string{"uploading", "processing", "ready"} {
		video := metadata.Video{ID: state, Title: state, Status: state, CreatedAt: time.Now().UTC()}
		if err := first.Create(ctx, video); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Create(ctx, metadata.Video{ID: "ready"}); err == nil {
		t.Fatal("duplicate create succeeded")
	}
	if _, err := first.Get(ctx, "missing"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal(err)
	}
	if err := first.SetStatus(ctx, "missing", "ready", ""); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal(err)
	}
	if err := first.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"uploading", "processing"} {
		v, err := second.Get(ctx, id)
		if err != nil || v.Status != "failed" || v.Error == "" {
			t.Fatal(v, err)
		}
	}
	file := cluster.File{Key: "video/segment.m4s", Size: 20, SHA256: "test-checksum", Owner: "a"}
	if err := first.PutFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	proposed := []cluster.Node{{ID: "a", Address: "unused:1"}, {ID: "b", Address: "unused:2"}}
	ring, err := hashring.New([]string{"a", "b"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ring.Owner(file.Key)
	if err != nil {
		t.Fatal(err)
	}
	bad := file
	bad.Size++
	bad.Owner = owner
	if err = first.CommitTopology(ctx, proposed, []cluster.File{bad}, cluster.Migration{State: "committed"}); err == nil {
		t.Fatal("stale inventory transaction succeeded")
	}
	nodes, err := second.Nodes(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatal("partial topology commit", nodes, err)
	}
	file.Owner = owner
	if err = first.CommitTopology(ctx, proposed, []cluster.File{file}, cluster.Migration{State: "committed"}); err != nil {
		t.Fatal(err)
	}
	got, err := second.File(ctx, file.Key)
	if err != nil || got != file {
		t.Fatal(got, err)
	}
	migration, err := second.Migration(ctx)
	if err != nil || migration.State != "committed" {
		t.Fatal(migration, err)
	}
	var wait sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			repo := first
			if i%2 == 1 {
				repo = second
			}
			if err := repo.Create(ctx, metadata.Video{ID: fmt.Sprintf("concurrent-%02d", i), Title: "Concurrent CAS", Status: "ready", CreatedAt: time.Now().UTC()}); err != nil {
				failures <- err
			}
		}(i)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	videos, err := second.List(ctx)
	if err != nil || len(videos) != 15 {
		t.Fatal("lost concurrent updates", len(videos), err)
	}
	if err = first.Create(ctx, metadata.Video{ID: "too-large", Title: strings.Repeat("x", etcdstore.MaxSnapshotBytes), CreatedAt: time.Now().UTC()}); err == nil {
		t.Fatal("oversized snapshot committed")
	}
	if _, err = second.Get(ctx, "too-large"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal("oversized write partially persisted", err)
	}
}

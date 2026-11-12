package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/hashring"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
)

// Opt in explicitly: this test builds images and starts disposable containers.
// A unique project/image and a dynamically published port isolate existing demos.
func TestDockerComposeLifecycle(t *testing.T) {
	if os.Getenv("TRITONTUBE_COMPOSE_TEST") != "1" {
		t.Skip("run make test-compose to exercise Docker images and health checks")
	}
	for _, tool := range []string{"docker", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required Compose test tool %s: %v", tool, err)
		}
	}
	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 10*time.Second)
	preflight, err := exec.CommandContext(preflightCtx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	preflightCancel()
	if err != nil {
		t.Fatalf("Docker daemon unavailable: %v\n%s", err, preflight)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	project := "tritontube-test-" + hex.EncodeToString(random)
	env := append(os.Environ(), "TRITONTUBE_IMAGE="+project+":test", "TRITONTUBE_HTTP_PORT=0")
	compose := func(ctx context.Context, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, "docker", append([]string{"compose", "--project-name", project, "--file", filepath.Join(root, "docker-compose.yml")}, args...)...)
		command.Env = env
		return command.CombinedOutput()
	}
	// Register cleanup before starting so partial startup failures are covered.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if t.Failed() {
			output, _ := compose(cleanupCtx, "--profile", "scale", "logs", "--no-color", "--tail", "80")
			t.Logf("Compose diagnostics:\n%s", output)
		}
		output, err := compose(cleanupCtx, "--profile", "scale", "down", "--volumes", "--remove-orphans", "--rmi", "all")
		if err != nil {
			t.Errorf("clean up isolated project %s: %v\n%s", project, err, output)
		}
	}()
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cancel()
		output, err := compose(ctx, args...)
		if err != nil {
			t.Fatalf("docker compose %v: %v\n%s", args, err, output)
		}
		return output
	}
	t.Logf("isolated Compose project: %s", project)
	run("up", "--build", "-d", "--wait", "--wait-timeout", "120", "web")
	address := strings.TrimSpace(string(run("port", "web", "8080")))
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("expected loopback-only published address: %q", address)
	}
	base := "http://" + address
	checkHealth := func() {
		t.Helper()
		var lastError string
		deadline := time.Now().Add(30 * time.Second)
		client := &http.Client{Timeout: 2 * time.Second}
		for time.Now().Before(deadline) {
			response, err := client.Get(base + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
				lastError = response.Status
			} else {
				lastError = err.Error()
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("HTTP metadata/storage health at %s did not recover: %s", base, lastError)
	}
	checkHealth()
	code, body := upload(t, base, generateVideo(t, true), "compose-sample.mp4")
	if code != 201 {
		t.Fatalf("container upload: %d %s", code, body)
	}
	var video metadata.Video
	if err = json.Unmarshal(body, &video); err != nil || video.Status != "ready" {
		t.Fatal("container metadata", video, err)
	}
	decodeDASH(t, base, video.ID, "Compose initial playback")
	var initial []cluster.Node
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "list"), &initial); err != nil || len(initial) != 3 {
		t.Fatal("initial container membership", initial, err)
	}
	ids := make([]string, 0, len(initial))
	for _, node := range initial {
		ids = append(ids, node.ID)
	}
	oldRing, err := hashring.New(ids, 100)
	if err != nil {
		t.Fatal(err)
	}
	manifestKey := video.ID + "/manifest.mpd"
	oldOwner, err := oldRing.Owner(manifestKey)
	if err != nil {
		t.Fatal(err)
	}
	joining := joiningID(t, initial, []cluster.File{{Key: manifestKey, Owner: oldOwner}})
	run("--profile", "scale", "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "storage4")
	var added cluster.Migration
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "add", joining, "storage4:9001"), &added); err != nil {
		t.Fatal(err)
	}
	if added.State != "committed" || added.Affected == 0 || added.Copied != added.Affected || len(added.CleanupErrors) > 0 {
		t.Fatal("container join", added)
	}
	decodeDASH(t, base, video.ID, "Compose playback after add")
	// The web process must recover four-node membership from the metadata volume.
	run("restart", "web")
	// HTTP readiness and Docker health status are separate asynchronous checks.
	// Wait for Docker's first successful probe instead of racing its start period.
	run("up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "60", "web")
	// A dynamic published port may be reassigned when Docker restarts a container.
	address = strings.TrimSpace(string(run("port", "web", "8080")))
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("invalid restarted published address: %q", address)
	}
	restartedBase := "http://" + address
	t.Logf("web restart address: %s → %s", base, restartedBase)
	base = restartedBase
	checkHealth()
	var restarted []cluster.Node
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "list"), &restarted); err != nil || len(restarted) != 4 {
		t.Fatal("container restart lost membership", restarted, err)
	}
	decodeDASH(t, base, video.ID, "Compose playback after web restart")
	var removed cluster.Migration
	if err = json.Unmarshal(run("exec", "-T", "web", "admin", "remove", joining), &removed); err != nil {
		t.Fatal(err)
	}
	if removed.State != "committed" || removed.Affected == 0 || removed.Copied != removed.Affected || len(removed.CleanupErrors) > 0 {
		t.Fatal("container removal", removed)
	}
	run("--profile", "scale", "stop", "storage4")
	checkHealth()
	decodeDASH(t, base, video.ID, "Compose playback after removal and node shutdown")
	run("exec", "-T", "web", "admin", "health")
	// Assert that Docker itself considers the web and original storage nodes healthy.
	for _, service := range []string{"web", "storage1", "storage2", "storage3"} {
		id := strings.TrimSpace(string(run("ps", "--quiet", service)))
		if id == "" {
			t.Fatal("missing container", service)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Health.Status}}", id).CombinedOutput()
		cancel()
		if err != nil || strings.TrimSpace(string(output)) != "healthy" {
			t.Fatalf("%s container health: %v %s", service, err, output)
		}
	}
	t.Logf("Compose upload/playback, %d-file join, four-node restart, %d-file graceful removal and Docker health checks passed", added.Copied, removed.Copied)
}

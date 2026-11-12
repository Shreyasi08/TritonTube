package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type fixture struct {
	http   *httptest.Server
	repo   *metadata.SQLite
	router *cluster.Router
	nodes  []cluster.Node
	work   string
	stops  map[string]func()
}

func storageNode(t *testing.T, id string) (cluster.Node, func()) {
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
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, hs)
	go server.Serve(listener)
	var once sync.Once
	stop := func() { once.Do(func() { server.Stop(); listener.Close(); store.Close() }) }
	t.Cleanup(stop)
	return cluster.Node{ID: id, Address: listener.Addr().String()}, stop
}
func setup(t *testing.T, processor func(*cluster.Router) api.Processor) *fixture {
	t.Helper()
	dir := t.TempDir()
	repo, err := metadata.Open(filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	var nodes []cluster.Node
	stops := map[string]func(){}
	for i := 1; i <= 3; i++ {
		node, stop := storageNode(t, fmt.Sprintf("node%d", i))
		nodes = append(nodes, node)
		stops[node.ID] = stop
	}
	if _, err = repo.InitializeNodes(context.Background(), nodes, 100); err != nil {
		t.Fatal(err)
	}
	router, err := cluster.New(nodes, 100, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(router.Close)
	work := filepath.Join(dir, "work")
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	var p api.Processor
	if processor != nil {
		p = processor(router)
	}
	server := httptest.NewServer(api.New(repo, router, work, p).Handler())
	t.Cleanup(server.Close)
	return &fixture{http: server, repo: repo, router: router, nodes: nodes, work: work, stops: stops}
}
func upload(t *testing.T, url string, data []byte, filename string) (int, []byte) {
	t.Helper()
	return uploadWithTitle(t, url, data, filename, "")
}
func uploadWithTitle(t *testing.T, url string, data []byte, filename, title string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if title != "" {
		if err := writer.WriteField("title", title); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(url+"/api/videos", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, result
}
func TestHTTPUploadAndMetadata(t *testing.T) {
	f := setup(t, nil)
	// This phase tests MP4 signature validation and source storage, not decoding.
	data := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte("payload"), 20000)...)
	code, body := upload(t, f.http.URL, data, "clip.mp4")
	if code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	var v metadata.Video
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != "uploaded" {
		t.Fatal(v)
	}
	var got bytes.Buffer
	if err := f.router.Download(context.Background(), v.ID+"/source.mp4", &got); err != nil || !bytes.Equal(data, got.Bytes()) {
		t.Fatal("source round trip", err)
	}
	for _, path := range []string{"/api/videos", "/api/videos/" + v.ID, "/healthz"} {
		resp, err := http.Get(f.http.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(path, resp.StatusCode)
		}
	}
	files, err := f.repo.Files(context.Background())
	if err != nil || len(files) != 1 || files[0].Owner == "" {
		t.Fatal(files, err)
	}
	entries, err := os.ReadDir(f.work)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary upload leaked", err)
	}
}
func TestInvalidUploadAndMissingVideo(t *testing.T) {
	f := setup(t, nil)
	for _, tc := range []struct {
		data []byte
		name string
	}{{[]byte("not video"), "bad.mp4"}, {[]byte("abcdefghijklmnop"), "bad.mp4"}, {[]byte("0000ftypisom"), "bad.txt"}} {
		code, body := upload(t, f.http.URL, tc.data, tc.name)
		if code != 400 {
			t.Fatalf("invalid upload: %d %s", code, body)
		}
	}
	resp, err := http.Get(f.http.URL + "/api/videos/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	videos, err := f.repo.List(context.Background())
	if err != nil || len(videos) != 0 {
		t.Fatal(videos, err)
	}
}

func generateVideo(t *testing.T, audio bool) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable; media integration test requires FFmpeg")
	}
	output := filepath.Join(t.TempDir(), "sample.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30"}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000")
	}
	args = append(args, "-t", "3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-threads", "2")
	if audio {
		args = append(args, "-c:a", "aac", "-shortest")
	}
	args = append(args, "-movflags", "+faststart", output)
	if data, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate media: %v %s", err, data)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestDASHEndToEnd(t *testing.T) {
	for _, audio := range []bool{false, true} {
		t.Run(fmt.Sprintf("audio=%t", audio), func(t *testing.T) {
			f := setup(t, func(r *cluster.Router) api.Processor { return &transcode.FFmpeg{Router: r} })
			title := "<script>alert(1)</script> & café"
			source := generateVideo(t, audio)
			code, body := uploadWithTitle(t, f.http.URL, source, "sample.mp4", title)
			if code != 201 {
				t.Fatalf("upload: %d %s", code, body)
			}
			var v metadata.Video
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			if v.Status != "ready" || v.Title != title || v.Size != int64(len(source)) {
				t.Fatal(v)
			}
			persisted, err := f.repo.Get(context.Background(), v.ID)
			if err != nil || persisted.Status != "ready" || persisted.Title != title {
				t.Fatal("SQLite video record", persisted, err)
			}
			listResp, err := http.Get(f.http.URL + "/api/videos")
			if err != nil {
				t.Fatal(err)
			}
			var library []metadata.Video
			err = json.NewDecoder(listResp.Body).Decode(&library)
			listResp.Body.Close()
			if err != nil || listResp.StatusCode != 200 || len(library) != 1 || library[0].ID != v.ID {
				t.Fatal("library API", library, err)
			}
			resp, err := http.Get(f.http.URL + "/stream/" + v.ID + "/manifest.mpd")
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode, err)
			}
			if !bytes.Contains(manifest, []byte("<MPD")) || bytes.Count(manifest, []byte("<Representation")) < 2 {
				t.Fatalf("invalid DASH manifest: %s", manifest)
			}
			var mpd struct {
				Sets []struct {
					ContentType     string `xml:"contentType,attr"`
					Representations []struct {
						Height int `xml:"height,attr"`
					} `xml:"Representation"`
				} `xml:"Period>AdaptationSet"`
			}
			if err = xml.Unmarshal(manifest, &mpd); err != nil {
				t.Fatal(err)
			}
			heights := map[int]bool{}
			for _, set := range mpd.Sets {
				if set.ContentType == "video" {
					for _, rep := range set.Representations {
						heights[rep.Height] = true
					}
				}
			}
			if len(heights) != 2 || !heights[360] || !heights[720] {
				t.Fatal("missing adaptive video representations", heights)
			}
			files, err := f.repo.Files(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			owners := map[string]bool{}
			for _, file := range files {
				owners[file.Owner] = true
				resp, err = http.Get(f.http.URL + "/stream/" + file.Key)
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || int64(len(content)) != file.Size {
					t.Fatal("media unavailable", file.Key, resp.StatusCode, err)
				}
			}
			if len(owners) < 2 {
				t.Fatal("media not distributed")
			}
			for _, path := range []string{"/", "/watch/" + v.ID, "/static/style.css", "/static/player.js"} {
				resp, err = http.Get(f.http.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				page, e := io.ReadAll(resp.Body)
				resp.Body.Close()
				if e != nil {
					t.Fatal(e)
				}
				if resp.StatusCode != 200 {
					t.Fatal(path, resp.StatusCode)
				}
				if path == "/watch/"+v.ID {
					if bytes.Contains(page, []byte(title)) || !bytes.Contains(page, []byte(html.EscapeString(title))) {
						t.Fatal("player title was not escaped")
					}
					if !bytes.Contains(page, []byte("dash.all.min.js")) || !bytes.Contains(page, []byte("/stream/"+v.ID+"/manifest.mpd")) {
						t.Fatal("player not wired to DASH manifest")
					}
				}
			}
			// Have FFmpeg independently fetch the HTTP manifest and segments and decode frames.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			result, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", f.http.URL+"/stream/"+v.ID+"/manifest.mpd", "-map", "0:v:0", "-map", "0:v:1", "-map", "0:a?", "-t", "1", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("DASH decode: %v %s", err, result)
			}
			t.Logf("MP4 %d bytes → SQLite ready record → %d inventoried files across %d nodes → HTTP DASH decoded (360p, 720p, audio=%t)", len(source), len(files), len(owners), audio)
			entries, err := os.ReadDir(f.work)
			if err != nil || len(entries) != 0 {
				t.Fatal("temporary media leaked", err)
			}
		})
	}
}
func TestTranscodeFailureIsRecorded(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe required")
	}
	f := setup(t, func(r *cluster.Router) api.Processor { return &transcode.FFmpeg{Router: r} })
	code, body := upload(t, f.http.URL, []byte("0000ftypisominvalid"), "invalid.mp4")
	if code != 422 {
		t.Fatalf("expected explicit decode failure: %d %s", code, body)
	}
	videos, err := f.repo.List(context.Background())
	if err != nil || len(videos) != 1 || videos[0].Status != "failed" || videos[0].Error == "" {
		t.Fatal(videos, err)
	}
	entries, err := os.ReadDir(f.work)
	if err != nil || len(entries) != 0 {
		t.Fatal("failure leaked temporary files", err)
	}
}

func seedFiles(t *testing.T, f *fixture) map[string][]byte {
	t.Helper()
	contents := map[string][]byte{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 80; i++ {
		key := fmt.Sprintf("migration/segment-%03d.m4s", i)
		data := bytes.Repeat([]byte(key), 1000)
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		if _, err := f.router.Upload(ctx, key, sum, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		contents[key] = data
	}
	return contents
}
func verifyFiles(t *testing.T, r *cluster.Router, contents map[string][]byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for key, want := range contents {
		var got bytes.Buffer
		if err := r.Download(ctx, key, &got); err != nil || !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("lost content %s: %v", key, err)
		}
	}
}
func TestGracefulAddRemoveAndRestart(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	before, err := f.repo.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fourth, _ := storageNode(t, "node4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := f.router.Add(ctx, fourth)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "committed" || result.Affected == 0 || result.Copied != result.Affected {
		t.Fatal(result)
	}
	verifyFiles(t, f.router, contents)
	after, err := f.repo.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]cluster.File{}
	for _, file := range before {
		old[file.Key] = file
	}
	for _, file := range after {
		if old[file.Key].Owner != file.Owner {
			for _, node := range f.nodes {
				if node.ID == old[file.Key].Owner {
					client, e := storage.Dial(node.Address)
					if e != nil {
						t.Fatal(e)
					}
					e = client.Download(ctx, file.Key, io.Discard)
					client.Close()
					if status.Code(e) != codes.NotFound {
						t.Fatal("old source not cleaned after commit", e)
					}
				}
			}
		}
	}
	result, err = f.router.Remove(ctx, "node1")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "committed" {
		t.Fatal(result)
	}
	verifyFiles(t, f.router, contents)
	nodes, err := f.repo.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := cluster.New(nodes, 100, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	verifyFiles(t, restarted, contents)
	inventory, err := f.repo.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range inventory {
		if file.Owner == "node1" {
			t.Fatal("removed owner retained")
		}
	}
}
func TestMigrationFailureRetainsSources(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	fourth, _ := storageNode(t, "node4")
	nextRing, err := hashring.New([]string{"node1", "node2", "node3", "node4"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var affected []cluster.File
	for _, file := range before {
		owner, _ := nextRing.Owner(file.Key)
		if owner != file.Owner {
			affected = append(affected, file)
		}
	}
	if len(affected) < 2 {
		t.Fatal("insufficient migration sample")
	}
	missing := affected[len(affected)-1]
	var missingClient *storage.Client
	for _, node := range f.nodes {
		if node.ID == missing.Owner {
			missingClient, err = storage.Dial(node.Address)
			if err != nil {
				t.Fatal(err)
			}
			defer missingClient.Close()
		}
	}
	if err = missingClient.Delete(context.Background(), missing.Key); err != nil {
		t.Fatal(err)
	}
	delete(contents, missing.Key)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := f.router.Add(ctx, fourth)
	if err == nil || result.State != "failed" || result.Copied == 0 {
		t.Fatal("failure not surfaced after copying", result, err)
	}
	if len(f.router.Nodes()) != 3 {
		t.Fatal("premature membership commit")
	}
	verifyFiles(t, f.router, contents)
	after, err := f.repo.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed ownership")
	}
	for _, file := range affected[:len(affected)-1] {
		for _, node := range f.nodes {
			if node.ID == file.Owner {
				client, e := storage.Dial(node.Address)
				if e != nil {
					t.Fatal(e)
				}
				e = client.Download(ctx, file.Key, io.Discard)
				client.Close()
				if e != nil {
					t.Fatal("source prematurely deleted", e)
				}
			}
		}
	}
	persisted, err := f.repo.Migration(ctx)
	if err != nil || persisted.State != "failed" {
		t.Fatal(persisted, err)
	}
}

type rejectCommit struct{ cluster.Inventory }

func (r rejectCommit) CommitTopology(context.Context, []cluster.Node, []cluster.File, cluster.Migration) error {
	return fmt.Errorf("injected metadata commit failure")
}
func TestMetadataCommitFailureRetainsRouting(t *testing.T) {
	f := setup(t, nil)
	contents := seedFiles(t, f)
	router, err := cluster.New(f.nodes, 100, rejectCommit{f.repo})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	fourth, _ := storageNode(t, "node4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := router.Add(ctx, fourth)
	if err == nil || result.State != "failed" || result.Copied != result.Affected {
		t.Fatal(result, err)
	}
	verifyFiles(t, router, contents)
	nodes, err := f.repo.Nodes(ctx)
	if err != nil || len(nodes) != 3 {
		t.Fatal("persisted topology changed", nodes, err)
	}
}

type blockedInventory struct {
	cluster.Inventory
	entered chan struct{}
	release chan struct{}
}

func (b blockedInventory) Files(ctx context.Context) ([]cluster.File, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.Inventory.Files(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestConcurrentTopologyChangesRejected(t *testing.T) {
	f := setup(t, nil)
	fourth, _ := storageNode(t, "node4")
	block := blockedInventory{Inventory: f.repo, entered: make(chan struct{}), release: make(chan struct{})}
	router, err := cluster.New(f.nodes, 100, block)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := router.Add(ctx, fourth); done <- err }()
	select {
	case <-block.entered:
	case <-ctx.Done():
		t.Fatal("migration did not start")
	}
	if _, err = router.Remove(ctx, "node1"); !errors.Is(err, cluster.ErrBusy) {
		close(block.release)
		t.Fatalf("concurrent topology change was not rejected: %v", err)
	}
	if router.Migration().State != "copying" {
		t.Fatal("migration status unavailable")
	}
	close(block.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentUploads(t *testing.T) {
	f := setup(t, nil)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := bytes.Repeat([]byte(fmt.Sprintf("concurrent-%d", i)), 10000)
			key := fmt.Sprintf("concurrent/video-%d.mp4", i)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			if _, err := f.router.Upload(ctx, key, sum, bytes.NewReader(data)); err != nil {
				errors <- err
				return
			}
			var got bytes.Buffer
			if err := f.router.Download(ctx, key, &got); err != nil {
				errors <- err
				return
			}
			if !bytes.Equal(data, got.Bytes()) {
				errors <- fmt.Errorf("corrupt concurrent upload %d", i)
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	files, err := f.repo.Files(context.Background())
	if err != nil || len(files) != 12 {
		t.Fatal(files, err)
	}
}
func TestStreamRangeAndMissingMedia(t *testing.T) {
	f := setup(t, nil)
	ctx := context.Background()
	v := metadata.Video{ID: "range-video", Title: "Ranges", Status: "ready", CreatedAt: time.Now().UTC()}
	if err := f.repo.Create(ctx, v); err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	if _, err := f.router.Upload(ctx, v.ID+"/segment.m4s", sum, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("GET", f.http.URL+"/stream/"+v.ID+"/segment.m4s", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=5-12")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 206 || !bytes.Equal(got, data[5:13]) {
		t.Fatal(resp.StatusCode, string(got), err)
	}
	resp, err = http.Get(f.http.URL + "/stream/" + v.ID + "/missing.m4s")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
}

func TestConcurrentHTTPUploads(t *testing.T) {
	f := setup(t, nil)
	data := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte("concurrent-http"), 30000)...)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code, body := upload(t, f.http.URL, data, "concurrent.mp4")
			if code != 201 {
				t.Errorf("concurrent HTTP upload: %d %s", code, body)
			}
			results <- code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for code := range results {
		if code != 201 {
			t.Fatal(code)
		}
	}
	videos, err := f.repo.List(context.Background())
	if err != nil || len(videos) != 2 || videos[0].ID == videos[1].ID {
		t.Fatal(videos, err)
	}
}

type rejectFinalStatus struct{ metadata.Repository }

func (r rejectFinalStatus) SetStatus(ctx context.Context, id, state, message string) error {
	if state == "uploaded" || state == "ready" {
		return fmt.Errorf("injected final metadata failure")
	}
	return r.Repository.SetStatus(ctx, id, state, message)
}
func TestUploadFinalMetadataFailureIsRecorded(t *testing.T) {
	f := setup(t, nil)
	server := httptest.NewServer(api.New(rejectFinalStatus{f.repo}, f.router, f.work, nil).Handler())
	defer server.Close()
	code, body := upload(t, server.URL, []byte("0000ftypisomtest-source"), "sample.mp4")
	if code != 500 {
		t.Fatalf("expected finalization failure: %d %s", code, body)
	}
	var result struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.Error == "" {
		t.Fatal("missing failure reference", string(body))
	}
	v, err := f.repo.Get(context.Background(), result.ID)
	if err != nil || v.Status != "failed" || v.Error == "" {
		t.Fatal(v, err)
	}
	resp, err := http.Get(server.URL + "/api/videos/" + result.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("failure metadata inaccessible", resp.StatusCode)
	}
	entries, err := os.ReadDir(f.work)
	if err != nil || len(entries) != 0 {
		t.Fatal("failure leaked temporary input", err)
	}
}

type rejectDASHFiles struct{ keys []string }

func (r *rejectDASHFiles) Upload(_ context.Context, key, _ string, _ io.Reader) (cluster.File, error) {
	r.keys = append(r.keys, key)
	return cluster.File{}, fmt.Errorf("injected destination failure")
}
func TestDASHPublishFailureCleansOutput(t *testing.T) {
	source := generateVideo(t, false)
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(input, source, 0600); err != nil {
		t.Fatal(err)
	}
	sink := &rejectDASHFiles{}
	pipeline := &transcode.FFmpeg{Router: sink}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := pipeline.Process(ctx, "video", input)
	if err == nil || !strings.Contains(err.Error(), "injected destination failure") {
		t.Fatal("publication failure not surfaced", err)
	}
	if len(sink.keys) != 1 || strings.HasSuffix(sink.keys[0], "manifest.mpd") {
		t.Fatal("manifest published before segments", sink.keys)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "input.mp4" {
		t.Fatal("failed publication leaked DASH output", entries, err)
	}
}
func TestEncoderFailureIsRecordedAndNotPlayable(t *testing.T) {
	source := generateVideo(t, true)
	missingBinary := filepath.Join(t.TempDir(), "missing-ffmpeg")
	f := setup(t, func(r *cluster.Router) api.Processor { return &transcode.FFmpeg{Router: r, Binary: missingBinary} })
	code, body := upload(t, f.http.URL, source, "sample.mp4")
	if code != 422 {
		t.Fatalf("expected encoder failure: %d %s", code, body)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	video, err := f.repo.Get(context.Background(), result.ID)
	if err != nil || video.Status != "failed" || !strings.Contains(video.Error, "ffmpeg:") {
		t.Fatal("encoder failure not recorded", video, err)
	}
	for _, path := range []string{"/watch/" + video.ID, "/stream/" + video.ID + "/manifest.mpd"} {
		resp, err := http.Get(f.http.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 409 {
			t.Fatalf("failed video playable: %s %d", path, resp.StatusCode)
		}
	}
	files, err := f.repo.Files(context.Background())
	if err != nil || len(files) != 1 || files[0].Key != video.ID+"/source.mp4" {
		t.Fatal("unexpected failure inventory", files, err)
	}
	entries, err := os.ReadDir(f.work)
	if err != nil || len(entries) != 0 {
		t.Fatal("encoder failure leaked temporary media", entries, err)
	}
}

func TestHealthzReportsDependencyFailures(t *testing.T) {
	for _, dependency := range []string{"storage", "metadata"} {
		t.Run(dependency, func(t *testing.T) {
			f := setup(t, nil)
			if dependency == "storage" {
				f.stops["node1"]()
			} else {
				if err := f.repo.Close(); err != nil {
					t.Fatal(err)
				}
			}
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(f.http.URL + "/healthz")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 503 {
				t.Fatalf("%s failure returned HTTP %d", dependency, response.StatusCode)
			}
		})
	}
}

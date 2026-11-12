// Package api implements the HTTP video and cluster API.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
)

type Processor interface {
	Process(context.Context, string, string) error
}
type Server struct {
	repo      metadata.Repository
	router    *cluster.Router
	work      string
	processor Processor
	maxUpload int64
	slots     chan struct{}
}

func New(repo metadata.Repository, router *cluster.Router, work string, processor Processor) *Server {
	return &Server{repo: repo, router: router, work: work, processor: processor, maxUpload: 512 << 20, slots: make(chan struct{}, 2)}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	s.uiRoutes(mux)
	s.clusterRoutes(mux)
	mux.HandleFunc("POST /api/videos", s.upload)
	mux.HandleFunc("GET /api/videos", s.list)
	mux.HandleFunc("GET /api/videos/{id}", s.get)
	return mux
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write response", "error", err)
	}
}
func fail(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.repo.Ping(ctx); err != nil {
		fail(w, 503, "metadata unavailable")
		return
	}
	if err := s.router.Ready(); err != nil {
		fail(w, 503, err.Error())
		return
	}
	checks := s.router.Health(ctx)
	healthy := true
	for _, node := range checks {
		healthy = healthy && node.Healthy
	}
	code := 200
	state := "ok"
	if !healthy {
		code = 503
		state = "degraded"
	}
	writeJSON(w, code, map[string]any{"status": state, "service": "TritonTube", "nodes": checks})
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	videos, err := s.repo.List(r.Context())
	if err != nil {
		fail(w, 500, "cannot list videos")
		return
	}
	writeJSON(w, 200, videos)
}
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	video, err := s.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, metadata.ErrNotFound) {
		fail(w, 404, "video not found")
		return
	}
	if err != nil {
		fail(w, 500, "metadata unavailable")
		return
	}
	writeJSON(w, 200, video)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if s.router != nil {
		if err := s.router.Ready(); err != nil {
			fail(w, 503, err.Error())
			return
		}
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		fail(w, 503, "upload capacity reached; try again shortly")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			fail(w, 413, "upload exceeds 512 MiB limit")
		} else {
			fail(w, 400, "invalid multipart upload")
		}
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "MP4 file field required")
		return
	}
	defer file.Close()
	if header.Size < 12 || header.Size > s.maxUpload || !strings.EqualFold(filepath.Ext(header.Filename), ".mp4") {
		fail(w, 400, "upload must be an MP4 of at most 512 MiB")
		return
	}
	var prefix [12]byte
	if _, err = io.ReadFull(file, prefix[:]); err != nil || string(prefix[4:8]) != "ftyp" {
		fail(w, 400, "invalid MP4 file signature")
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		fail(w, 500, "cannot read upload")
		return
	}
	dir, err := os.MkdirTemp(s.work, "upload-")
	if err != nil {
		fail(w, 500, "cannot create workspace")
		return
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input.mp4")
	output, err := os.OpenFile(input, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(w, 500, "cannot save upload")
		return
	}
	h := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(output, h), file)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		fail(w, 500, "cannot save upload")
		return
	}
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		fail(w, 500, "cannot allocate video ID")
		return
	}
	id := hex.EncodeToString(idBytes)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	if len(title) > 200 {
		fail(w, 400, "title exceeds 200 bytes")
		return
	}
	video := metadata.Video{ID: id, Title: title, Status: "uploading", CreatedAt: time.Now().UTC(), Size: size}
	if err = s.repo.Create(ctx, video); err != nil {
		fail(w, 500, "cannot create metadata")
		return
	}
	w.Header().Set("Location", "/api/videos/"+id)
	processErr := s.saveSource(ctx, id, input, hex.EncodeToString(h.Sum(nil)))
	if processErr == nil && s.processor != nil {
		if processErr = s.repo.SetStatus(ctx, id, "processing", ""); processErr == nil {
			processErr = s.processor.Process(ctx, id, input)
		}
		video.Status = "ready"
	} else {
		video.Status = "uploaded"
	}
	if processErr != nil {
		s.failUpload(w, id, 422, processErr, "video processing failed; inspect video metadata and server logs")
		return
	}
	if err = s.repo.SetStatus(ctx, id, video.Status, ""); err != nil {
		s.failUpload(w, id, 500, err, "cannot finalize video metadata")
		return
	}
	writeJSON(w, 201, video)
}
func (s *Server) saveSource(ctx context.Context, id, input, checksum string) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = s.router.Upload(ctx, id+"/source.mp4", checksum, f)
	if err != nil {
		return fmt.Errorf("store source: %w", err)
	}
	return nil
}

// Record failures with an independent deadline even when the upload request was
// canceled. Include the ID so the caller can query the persisted failure record.
func (s *Server) failUpload(w http.ResponseWriter, id string, code int, cause error, message string) {
	slog.Error("video upload failed", "video_id", id, "error", cause)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.repo.SetStatus(ctx, id, "failed", cause.Error()); err != nil {
		slog.Error("persist failure status", "video_id", id, "error", err)
	}
	writeJSON(w, code, map[string]string{"id": id, "error": message})
}

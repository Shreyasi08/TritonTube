package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/metadata"
	"github.com/Shreyasi08/TritonTube/internal/storage"
	assets "github.com/Shreyasi08/TritonTube/web"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var templates = template.Must(template.ParseFS(assets.Assets, "templates/*.html"))

func (s *Server) uiRoutes(mux *http.ServeMux) {
	static, err := fs.Sub(assets.Assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templates.ExecuteTemplate(w, "library.html", nil); err != nil {
			slog.Error("render library", "error", err)
		}
	})
	mux.HandleFunc("GET /watch/{id}", s.watch)
	mux.HandleFunc("GET /stream/{id}/{filename}", s.stream)
}
func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	video, err := s.repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, metadata.ErrNotFound) {
		fail(w, 404, "video not found")
		return
	}
	if err != nil {
		fail(w, 500, "metadata unavailable")
		return
	}
	if video.Status != "ready" {
		fail(w, 409, "video is not ready for playback")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err = templates.ExecuteTemplate(w, "watch.html", video); err != nil {
		slog.Error("render player", "error", err)
	}
}

type boundedWriter struct {
	w         io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("stored file exceeds inventory size")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	id, name := r.PathValue("id"), r.PathValue("filename")
	key := id + "/" + name
	if path.Base(name) != name || strings.Contains(id, "/") || storage.ValidateKey(key) != nil {
		fail(w, 400, "invalid stream path")
		return
	}
	video, err := s.repo.Get(ctx, id)
	if errors.Is(err, metadata.ErrNotFound) {
		fail(w, 404, "video not found")
		return
	}
	if err != nil {
		fail(w, 500, "metadata unavailable")
		return
	}
	if video.Status != "ready" {
		fail(w, 409, "video is not ready")
		return
	}
	info, err := s.repo.File(ctx, key)
	if errors.Is(err, metadata.ErrNotFound) {
		fail(w, 404, "media file not found")
		return
	}
	if err != nil {
		fail(w, 500, "inventory unavailable")
		return
	}
	file, err := os.CreateTemp(s.work, "stream-")
	if err != nil {
		fail(w, 500, "cannot create stream workspace")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	writer := &boundedWriter{w: io.MultiWriter(file, hash), remaining: info.Size}
	if err = s.router.Download(ctx, key, writer); err != nil {
		if status.Code(err) == codes.NotFound {
			fail(w, 404, "media file missing from storage")
		} else {
			fail(w, 502, "storage unavailable")
		}
		slog.Error("stream download", "key", key, "error", err)
		return
	}
	if writer.remaining != 0 || hex.EncodeToString(hash.Sum(nil)) != info.SHA256 {
		fail(w, 502, "stored file failed checksum verification")
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		fail(w, 500, "cannot read media")
		return
	}
	switch path.Ext(name) {
	case ".mpd":
		w.Header().Set("Content-Type", "application/dash+xml")
	case ".m4s":
		w.Header().Set("Content-Type", "video/iso.segment")
	case ".mp4":
		w.Header().Set("Content-Type", "video/mp4")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("ETag", `"`+info.SHA256+`"`)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, video.CreatedAt, file)
}

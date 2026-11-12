package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
)

func (s *Server) clusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cluster/nodes", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.router.Nodes()) })
	mux.HandleFunc("GET /api/cluster/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.router.Health(r.Context())) })
	mux.HandleFunc("GET /api/cluster/migration", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.router.Migration()) })
	mux.HandleFunc("POST /api/cluster/nodes", s.addNode)
	mux.HandleFunc("DELETE /api/cluster/nodes/{id}", s.removeNode)
}
func (s *Server) addNode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var node cluster.Node
	if err := decoder.Decode(&node); err != nil {
		fail(w, 400, "expected node id and address JSON")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "unexpected trailing JSON")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	result, err := s.router.Add(ctx, node)
	s.migrationResponse(w, result, err)
}
func (s *Server) removeNode(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	result, err := s.router.Remove(ctx, r.PathValue("id"))
	s.migrationResponse(w, result, err)
}
func (s *Server) migrationResponse(w http.ResponseWriter, result cluster.Migration, err error) {
	if errors.Is(err, cluster.ErrBusy) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error(), "migration": result})
		return
	}
	writeJSON(w, 200, result)
}

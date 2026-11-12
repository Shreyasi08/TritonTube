package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminHealthExitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cluster/health" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"node":{"id":"offline","address":"127.0.0.1:1"},"healthy":false,"error":"connection refused"}]`))
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := run([]string{"-url", server.URL, "health"}, &out); err == nil {
		t.Fatal("health command should fail for an unhealthy node")
	}
	if !bytes.Contains(out.Bytes(), []byte("offline")) {
		t.Fatal(out.String())
	}
}

func TestAdminRejectsUncommittedMembershipResponse(t *testing.T) {
	for _, command := range [][]string{{"add", "node4", "127.0.0.1:9004"}, {"remove", "node1"}} {
		t.Run(command[0], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"state":"failed","error":"checksum mismatch"}`))
			}))
			defer server.Close()
			args := append([]string{"-url", server.URL}, command...)
			var out bytes.Buffer
			if err := run(args, &out); err == nil {
				t.Fatal("uncommitted migration reported as success")
			}
		})
	}
}

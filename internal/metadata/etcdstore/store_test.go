package etcdstore

import (
	"github.com/Shreyasi08/TritonTube/internal/metadata"
	"strings"
	"testing"
)

func TestSnapshotBoundsAndConfiguration(t *testing.T) {
	state := fresh()
	state.Videos["large"] = metadata.Video{ID: "large", Title: strings.Repeat("x", MaxSnapshotBytes)}
	if _, err := encode(state); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	for _, prefix := range []string{"", "/", "relative", "/a/../b"} {
		if client, err := Open([]string{"http://localhost:2379"}, prefix); err == nil {
			client.Close()
			t.Fatal("invalid prefix accepted", prefix)
		}
	}
	if client, err := Open(nil, "/test"); err == nil {
		client.Close()
		t.Fatal("missing endpoints accepted")
	}
}

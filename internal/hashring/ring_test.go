package hashring

import (
	"fmt"
	"reflect"
	"testing"
)

func TestDeterminismAndRemapping(t *testing.T) {
	a, _ := New([]string{"a", "b", "c"}, 100)
	b, _ := New([]string{"c", "a", "b"}, 100)
	added, _ := New([]string{"a", "b", "c", "d"}, 100)
	if !reflect.DeepEqual(a.points, b.points) {
		t.Fatal("insertion order changed ring")
	}
	moved := 0
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("video/segment-%d", i)
		x, _ := a.Owner(key)
		y, _ := b.Owner(key)
		z, _ := added.Owner(key)
		if x != y {
			t.Fatal("not deterministic")
		}
		if x != z {
			moved++
			if z != "d" {
				t.Fatal("moved between existing nodes")
			}
		}
	}
	if moved < 1500 || moved > 3500 {
		t.Fatalf("unexpected remapping %d/10000", moved)
	}
	if Hash("abc") != 0xba7816bf8f01cfea {
		t.Fatal("SHA-256 truncation changed")
	}
}
func TestBoundariesAndWraparound(t *testing.T) {
	r := &Ring{points: []point{{10, "a", 0}, {20, "b", 0}, {30, "c", 0}}}
	for _, tc := range []struct {
		pos  uint64
		want string
	}{{0, "a"}, {10, "a"}, {11, "b"}, {20, "b"}, {21, "c"}, {30, "c"}, {31, "a"}, {^uint64(0), "a"}} {
		got, err := r.ownerAt(tc.pos)
		if err != nil || got != tc.want {
			t.Fatalf("position %d: %s, %v", tc.pos, got, err)
		}
	}
}
func TestCollisionsAndValidation(t *testing.T) {
	r, err := newRing([]string{"z", "a"}, 3, func(string) uint64 { return 7 })
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := r.Owner("key")
	if owner != "a" {
		t.Fatal("collision order", owner)
	}
	empty, _ := New(nil, 100)
	if _, err = empty.Owner("key"); err != ErrEmpty {
		t.Fatal(err)
	}
	for _, nodes := range [][]string{{""}, {"a", "a"}} {
		if _, err = New(nodes, 100); err == nil {
			t.Fatal("invalid nodes accepted")
		}
	}
	if _, err = New([]string{"a"}, 0); err == nil {
		t.Fatal("invalid replica count accepted")
	}
}

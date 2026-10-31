package app

import (
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/platform"
)

func TestMarkersNeverCollideAcrossWorkspaces(t *testing.T) {
	a := &Engine{opt: Options{Dir: "/tmp/ws-a"}}
	b := &Engine{opt: Options{Dir: "/tmp/ws-b"}}
	seen := map[string]bool{}
	for _, e := range []*Engine{a, b} {
		m := markerFor(e.opt.Dir, 1, launchSeq.Add(1))
		if !platform.ValidMarker(m) || seen[m] {
			t.Fatalf("marker %q invalid or reused", m)
		}
		seen[m] = true
	}
	// the same launch number in two workspaces still differs
	if markerFor("/tmp/ws-a", 1, 1) == markerFor("/tmp/ws-b", 1, 1) {
		t.Fatal("workspaces share a marker, so one engine would reap the other's workers")
	}
	if !strings.HasPrefix(markerFor("/tmp/ws-a", 7, 3), markerFor("/tmp/ws-a", 7, 4)[:len("tacet_")+10+3]) {
		t.Fatal("a workspace's markers must share a prefix for the startup scan")
	}
}

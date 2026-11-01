//go:build darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Active is aggregated over all members, independently of the visible file page
// and the coarse verdict (which also calls paused files "processing").
func TestTrayActivityFollowsPauseResumeAndCancelBeyondTheVisiblePage(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	e.queue.PauseDispatch(true)
	in, out := t.TempDir(), t.TempDir()
	var paths []string
	for _, name := range []string{"a.txt", "b.txt"} {
		p := filepath.Join(in, name)
		if err := os.WriteFile(p, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: paths, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		got, err := e.Status(ctx, st.ID, 1)
		if err != nil || got.Active != want || !got.Truncated || len(got.Files) != 1 {
			t.Fatalf("want %d active: %+v %v", want, got, err)
		}
	}
	check(2)
	if err := e.Pause(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	check(0)
	if err := e.Resume(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	check(2)
	if err := e.Cancel(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	check(0)
}

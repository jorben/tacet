//go:build darwin

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBatchOf200Files runs a 200-file batch through the real engine and plugins. It is a measurement, not a
// regression test, so it only runs when TACET_SCALE=1.
func TestBatchOf200Files(t *testing.T) {
	if os.Getenv("TACET_SCALE") != "1" {
		t.Skip("set TACET_SCALE=1")
	}
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	var paths []string
	for i := 0; i < 160; i++ {
		var b strings.Builder
		for j := 0; j < 200; j++ {
			fmt.Fprintf(&b, "第%d行 联系 138%08d 邮箱 u%dx%d@corp.cn\n", j, (i*7+j)%100000000, i, j)
		}
		p := filepath.Join(in, fmt.Sprintf("t%03d.txt", i))
		os.WriteFile(p, []byte(b.String()), 0o600)
		paths = append(paths, p)
	}
	for i := 0; i < 40; i++ {
		var b strings.Builder
		b.WriteString("name,phone\n")
		for j := 0; j < 300; j++ {
			fmt.Fprintf(&b, "员工%d,139%08d\n", j, (i*13+j)%100000000)
		}
		p := filepath.Join(in, fmt.Sprintf("c%03d.csv", i))
		os.WriteFile(p, []byte(b.String()), 0o600)
		paths = append(paths, p)
	}
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	start := time.Now()
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: paths, OutputDir: out, Preset: PresetID})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: PresetID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("200 files in %s: %v exit=%d delivered=%d heapSys=%dMiB", time.Since(start).Round(time.Second), st.Counts, st.ExitCode, st.Delivered, ms.HeapSys>>20)
	n := 0
	for _, f := range st.Files {
		if f.State != "completed" {
			t.Errorf("%s %s %s", f.Name, f.State, f.Detail)
		} else {
			n++
		}
	}
	entries, _ := os.ReadDir(out)
	t.Logf("completed=%d output files=%d", n, len(entries))
}

//go:build darwin

package preview

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/testenv"
)

const previewFixture = `
import sys
sys.path.insert(0, sys.argv[1])
from pdf_fixtures import build
open(sys.argv[2], 'wb').write(build(['Call 13812345678 today', 'second page']))
`

func taskKey(string) ([]byte, error) { return bytes.Repeat([]byte{7}, 32), nil }

// A real PDF plugin renders a page through inherited handles; the host reads the PNG back from the
// transient write, decodes it, and finds the marked range placed on the page.
func TestRealPDFPagePreviewThroughInheritedHandles(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	pdf := filepath.Join(t.TempDir(), "in.pdf")
	if out, err := exec.Command(python, "-X", "utf8", "-c", previewFixture, filepath.Join(root, "python", "tests"), pdf).CombinedOutput(); err != nil {
		testenv.Missing(t, "the fixture, which the plugin interpreter builds: %v %s", err, out)
	}
	raw, _ := os.ReadFile(pdf)
	dir := t.TempDir()
	m, err := refs.New(dir, func(kernel.AttemptRef) error { return nil }, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	seed := kernel.AttemptRef{JobID: "j", AttemptID: "seed", OwnerGeneration: 9, JobVersion: 1}
	g0, err := m.IssueWrite(seed, 1<<26)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := g0.HostWriter(1 << 26)
	w.Write(raw)
	w.Close()
	if _, err = g0.Freeze(); err != nil {
		t.Fatal(err)
	}
	a := kernel.AttemptRef{JobID: "j", AttemptID: "p1", OwnerGeneration: 9, JobVersion: 1}
	defer m.Revoke(a)
	in, err := m.IssueRead(a, g0.ResourceID())
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.IssueWrite(a, MaxPNG)
	if err != nil {
		t.Fatal(err)
	}
	files, wires, err := refs.Handoff(in, out)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := host.Start(host.Spec{Path: python, Args: []string{"-I", "-m", "tacet_format_pdf"}, Dir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: files, Generation: "gen_preview_e2e_1"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = worker.Session.Initialize(ctx, "tacet.pdf", "0.1.0", 9); err != nil {
		t.Fatal(err)
	}
	pager, err := NewPager()
	if err != nil {
		t.Fatal(err)
	}
	var png_ []byte
	pg, err := pager.Render(ctx, worker.Session, a, wires[0], wires[1], 1, 100,
		[]Mark{{ID: "f1", Ordinal: 1, Start: 5, End: 16}, {ID: "f2", Ordinal: 7, Start: 0, End: 3}},
		func() (int64, error) {
			png_, err = out.ReadBack(MaxPNG)
			return int64(len(png_)), err
		})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(png_))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != pg.WidthPx || b.Dy() != pg.HeightPx || pg.Pages != 2 || pg.Page != 1 {
		t.Fatalf("%+v vs image %v", pg, b)
	}
	if len(pg.Boxes) == 0 || pg.Boxes[0].ID != "f1" || len(pg.Unmapped) != 1 || pg.Unmapped[0] != "f2" {
		t.Fatalf("%+v", pg)
	}
	// a page that does not exist is a failure, not an empty image
	_, err = pager.Render(ctx, worker.Session, a, wires[0], wires[1], 9, 100, nil, func() (int64, error) { return 0, nil })
	if !errors.Is(err, ErrPageProtocol) && !errors.Is(err, ErrPageFailed) {
		t.Fatalf("page 9: %v", err)
	}
}

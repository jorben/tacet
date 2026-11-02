//go:build darwin

package preflight

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/testenv"
)

const pdfFixtures = `
import sys
sys.path.insert(0, sys.argv[1])
from pdf_fixtures import build
d = sys.argv[2]
open(d + '/plain.pdf', 'wb').write(build(['one', 'two', 'three']))
open(d + '/locked.pdf', 'wb').write(build(['secret 13812345678'], user=b'correct horse'))
open(d + '/restricted.pdf', 'wb').write(build(['x'], user=b'', permissions=-3904))
open(d + '/long.pdf', 'wb').write(build(['p'] * 5))
`

type pdfRig struct {
	t      *testing.T
	python string
	dir    string
	n      int
}

func newPDFRig(t *testing.T) *pdfRig {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	dir := t.TempDir()
	if out, err := exec.Command(python, "-X", "utf8", "-c", pdfFixtures, filepath.Join(root, "python", "tests"), dir).CombinedOutput(); err != nil {
		testenv.Missing(t, "fixtures, which the plugin interpreter builds: %v %s", err, out)
	}
	return &pdfRig{t: t, python: python, dir: dir}
}

// start launches the PDF plugin with the named fixture as an encrypted read-only
// input (handle 3) and, if withOutput, an append-only encrypted output (handle 4).
func (r *pdfRig) start(name string, withOutput bool) (*host.Worker, refs.Wire, refs.Wire, []byte, string, context.Context) {
	t := r.t
	t.Helper()
	r.n++
	raw, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	inKey, outKey := make([]byte, 32), make([]byte, 32)
	rand.Read(inKey)
	rand.Read(outKey)
	inPath := filepath.Join(r.dir, fmt.Sprintf("res-%d", r.n))
	f, err := os.OpenFile(inPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.NewResourceWriter(f, inKey, "in", 1<<28)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(raw)
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	in, err := os.Open(inPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	files := []*os.File{in}
	outPath := filepath.Join(r.dir, fmt.Sprintf("out-%d", r.n))
	if withOutput {
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close() })
		files = append(files, out)
	}
	worker, err := host.Start(host.Spec{Path: r.python, Args: []string{"-I", "-m", "tacet_format_pdf"}, Dir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: files, Generation: "gen_pdf_e2e_test_1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { worker.Terminate(time.Second) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	if err = worker.Session.Initialize(ctx, "tacet.pdf", "0.1.0", 9); err != nil {
		t.Fatal(err)
	}
	return worker, refs.Wire{Ref: "r_in", Mode: refs.Read, Handle: 3, Version: 1, Encryption: enc(inKey, "in")},
		refs.Wire{Ref: "r_out", Mode: refs.Write, Handle: 4, Encryption: enc(outKey, "out")}, outKey, outPath, ctx
}

var attemptPDF = kernel.AttemptRef{JobID: "j", AttemptID: "a1", OwnerGeneration: 9, JobVersion: 1}

func TestRealPDFProbeThenDecide(t *testing.T) {
	r := newPDFRig(t)
	accepted := map[string]bool{"pdf": true}
	for name, want := range map[string]struct {
		probe Probe
		state State
		why   string
	}{
		"plain.pdf":      {Probe{Format: "pdf", Encryption: None, Pages: 3}, Preflighted, ""},
		"locked.pdf":     {Probe{Format: "pdf", Encryption: PasswordRequired}, Locked, ""},
		"restricted.pdf": {Probe{Format: "pdf", Encryption: PermissionOnly, Pages: 1}, Preflighted, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r.t = t
			worker, in, _, _, _, ctx := r.start(name, false)
			got, err := (&PluginProber{Session: worker.Session, Attempt: attemptPDF, Input: in}).Probe(ctx)
			if err != nil || got.Format != want.probe.Format || got.Encryption != want.probe.Encryption || got.Pages != want.probe.Pages {
				t.Fatalf("%+v %v", got, err)
			}
			o, err := Decide(got, accepted, DefaultLimits())
			if err != nil || o.State != want.state || o.Reason != want.why {
				t.Fatalf("%+v %v", o, err)
			}
		})
	}
	// a document over the page limit is reported with its real page count and refused by Decide
	r.t = t
	worker, in, _, _, _, ctx := r.start("long.pdf", false)
	got, err := (&PluginProber{Session: worker.Session, Attempt: attemptPDF, Input: in}).Probe(ctx)
	if err != nil || got.Pages != 5 {
		t.Fatalf("%+v %v", got, err)
	}
	o, _ := Decide(got, accepted, Limits{MaxPages: 3, MaxBytes: 1 << 30})
	if o.State != Unprocessable || o.Reason != ReasonPages || o.Actual != 5 || o.Limit != 3 {
		t.Fatalf("%+v", o)
	}
}

func TestRealPDFUnlockThroughInheritedHandles(t *testing.T) {
	r := newPDFRig(t)
	run := func(t *testing.T, password string) (string, error, []byte, int) {
		r.t = t
		worker, in, out, outKey, outPath, ctx := r.start("locked.pdf", true)
		sealed := 0
		u := &PluginUnlocker{Session: worker.Session, Attempt: attemptPDF, Input: in, Output: out,
			Seal: func() (string, error) { sealed++; return "res_copy", nil }}
		pw := []byte(password)
		ref, err := Unlock(ctx, u, "r_in", pw)
		for _, c := range pw {
			if c != 0 {
				t.Error("the caller's password survived")
				break
			}
		}
		var plain []byte
		if info, serr := os.Stat(outPath); serr == nil && info.Size() > 0 {
			of, _ := os.Open(outPath)
			defer of.Close()
			if rd, rerr := store.OpenResource(of, info.Size(), outKey, "out"); rerr == nil {
				plain = make([]byte, rd.Size())
				rd.ReadAt(plain, 0)
			}
		}
		return ref, err, plain, sealed
	}
	t.Run("correct", func(t *testing.T) {
		ref, err, plain, sealed := run(t, "correct horse")
		if err != nil || ref != "res_copy" || sealed != 1 {
			t.Fatalf("%q %v %d", ref, err, sealed)
		}
		if !bytes.HasPrefix(plain, []byte("%PDF-")) || bytes.Contains(plain, []byte("/Encrypt")) {
			t.Fatalf("the copy is not an unencrypted PDF (%d bytes)", len(plain))
		}
	})
	t.Run("wrong", func(t *testing.T) {
		ref, err, plain, sealed := run(t, "wrong")
		if err != ErrPasswordWrong || ref != "" || sealed != 0 || len(plain) != 0 {
			t.Fatalf("%q %v sealed=%d output=%d", ref, err, sealed, len(plain))
		}
	})
}

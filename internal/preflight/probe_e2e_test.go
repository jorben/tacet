//go:build darwin

package preflight

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
)

const probeFixtures = `
import io, sys, zipfile
sys.path.insert(0, sys.argv[1])
from test_docx_extract import package
from test_docx_unlock import encrypted, sized
d = sys.argv[2]
plain = package('<w:p><w:r><w:t>x</w:t></w:r></w:p>')
buf = io.BytesIO()
with zipfile.ZipFile(io.BytesIO(plain)) as zin, zipfile.ZipFile(buf, 'w') as zout:
    for n in zin.namelist(): zout.writestr(n, zin.read(n))
    zout.writestr('word/media/a.png', b'p' * 10); zout.writestr('word/embeddings/o.bin', b'o' * 5)
open(d + '/plain.docx', 'wb').write(buf.getvalue())
open(d + '/locked.docx', 'wb').write(encrypted(sized(plain), 'pw'))
open(d + '/notes.docx', 'wb').write(b'this is plain text with a docx extension')
`

// The real plugin classifies by content, then Decide judges the result.
func TestRealDOCXProbeThenDecide(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := filepath.Join(root, "python", ".venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
	dir := t.TempDir()
	if out, err := exec.Command(python, "-X", "utf8", "-c", probeFixtures, filepath.Join(root, "python", "tests"), dir).CombinedOutput(); err != nil {
		t.Skipf("cannot build fixtures: %v %s", err, out)
	}
	probe := func(name string) (Probe, error) {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		key := make([]byte, 32)
		rand.Read(key)
		path := filepath.Join(dir, "res-"+name)
		f, _ := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		w, _ := store.NewResourceWriter(f, key, "in", 1<<28)
		w.Write(raw)
		w.Close()
		f.Close()
		in, _ := os.Open(path)
		defer in.Close()
		worker, err := host.Start(host.Spec{Path: python, Args: []string{"-I", "-m", "tacet_format_docx"}, Dir: t.TempDir(),
			Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: []*os.File{in}, Generation: "gen_probe_e2e_test1"})
		if err != nil {
			t.Fatal(err)
		}
		defer worker.Terminate(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err = worker.Session.Initialize(ctx, "tacet.docx", "0.1.0", 9); err != nil {
			t.Fatal(err)
		}
		return (&PluginProber{Session: worker.Session, Attempt: kernel.AttemptRef{JobID: "j", AttemptID: "a1", OwnerGeneration: 9, JobVersion: 1},
			Input: refs.Wire{Ref: "r_in", Mode: refs.Read, Handle: 3, Version: 1, Encryption: enc(key, "in")}}).Probe(ctx)
	}
	accepted := map[string]bool{"docx": true}
	for name, want := range map[string]struct {
		probe Probe
		state State
		why   string
	}{
		"plain.docx":  {Probe{Format: "docx", Encryption: None, Images: 1, Embedded: 1}, Preflighted, ""},
		"locked.docx": {Probe{Format: "docx", Encryption: PasswordRequired}, Locked, ""},
		"notes.docx":  {Probe{Format: "unknown", Encryption: None}, Unprocessable, ReasonFormat},
	} {
		got, err := probe(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Format != want.probe.Format || got.Encryption != want.probe.Encryption || got.Images != want.probe.Images || got.Embedded != want.probe.Embedded {
			t.Errorf("%s: %+v", name, got)
		}
		o, err := Decide(got, accepted, DefaultLimits())
		if err != nil || o.State != want.state || o.Reason != want.why {
			t.Errorf("%s: %+v %v", name, o, err)
		}
	}
}

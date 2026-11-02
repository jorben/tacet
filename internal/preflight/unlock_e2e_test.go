//go:build darwin

package preflight

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

const encryptFixture = `
import io, os, sys, zipfile
sys.path.insert(0, sys.argv[1])
from test_docx_extract import package
from test_docx_unlock import encrypted, sized
open(sys.argv[2], 'wb').write(encrypted(sized(package('<w:p><w:r><w:t>13812345678</w:t></w:r></w:p>')), 'correct horse'))
`

func enc(key []byte, id string) *refs.Encryption {
	return &refs.Encryption{Version: 1, ResourceID: id, KeyHex: hex.EncodeToString(key)}
}

// A real plugin process decrypts a real encrypted package through inherited
// handles; the password crosses only as a host Secret.
func TestRealDOCXUnlockThroughInheritedHandles(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	python := testenv.Python(t)
	dir := t.TempDir()
	raw := filepath.Join(dir, "enc.docx")
	if out, err := exec.Command(python, "-X", "utf8", "-c", encryptFixture, filepath.Join(root, "python", "tests"), raw).CombinedOutput(); err != nil {
		testenv.Missing(t, "the encrypted fixture, which the plugin interpreter builds: %v %s", err, out)
	}
	original, _ := os.ReadFile(raw)

	run := func(password string) (string, error, []byte, []byte, string) {
		inKey, outKey := make([]byte, 32), make([]byte, 32)
		rand.Read(inKey)
		rand.Read(outKey)
		inPath, outPath := filepath.Join(dir, "in-"+password), filepath.Join(dir, "out-"+password)
		f, _ := os.OpenFile(inPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		w, err := store.NewResourceWriter(f, inKey, "in", 1<<28)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(original)
		w.Close()
		f.Close()
		in, _ := os.Open(inPath)
		defer in.Close()
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		worker, err := host.Start(host.Spec{Path: python, Args: []string{"-I", "-m", "tacet_format_docx"}, Dir: t.TempDir(),
			Env: []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}, ExtraFiles: []*os.File{in, out}, Generation: "gen_unlock_e2e_test1"})
		if err != nil {
			t.Fatal(err)
		}
		defer worker.Terminate(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err = worker.Session.Initialize(ctx, "tacet.docx", "0.1.0", 9); err != nil {
			t.Fatal(err)
		}
		sealed := 0
		u := &PluginUnlocker{Session: worker.Session, Attempt: kernel.AttemptRef{JobID: "j", AttemptID: "a1", OwnerGeneration: 9, JobVersion: 1},
			Input:  refs.Wire{Ref: "r_in", Mode: refs.Read, Handle: 3, Version: 1, Encryption: enc(inKey, "in")},
			Output: refs.Wire{Ref: "r_out", Mode: refs.Write, Handle: 4, Encryption: enc(outKey, "out")},
			Seal:   func() (string, error) { sealed++; return "res_copy", nil }}
		pw := []byte(password)
		ref, err := Unlock(ctx, u, "r_in", pw)
		var plain []byte
		if info, serr := os.Stat(outPath); serr == nil && info.Size() > 0 {
			of, _ := os.Open(outPath)
			defer of.Close()
			if r, rerr := store.OpenResource(of, info.Size(), outKey, "out"); rerr == nil {
				plain = make([]byte, r.Size())
				r.ReadAt(plain, 0)
			}
		}
		for _, c := range pw {
			if c != 0 {
				t.Errorf("the caller's password survived")
				break
			}
		}
		return ref, err, plain, outKey, map[int]string{0: "never", 1: "once"}[sealed]
	}

	ref, err, plain, _, sealed := run("correct horse")
	if err != nil || ref != "res_copy" || sealed != "once" {
		t.Fatalf("%q %v sealed=%s", ref, err, sealed)
	}
	if len(plain) == 0 || !bytes.HasPrefix(plain, []byte("PK")) || bytes.Contains(original, []byte("PK\x03\x04")) {
		t.Fatalf("the decrypted copy is not a package (%d bytes)", len(plain))
	}
	ref, err, plain, _, sealed = run("wrong")
	if err != ErrPasswordWrong || ref != "" || sealed != "never" {
		t.Fatalf("wrong password: %q %v sealed=%s", ref, err, sealed)
	}
	if len(plain) != 0 {
		t.Fatalf("a wrong password left %d bytes of output", len(plain))
	}
}

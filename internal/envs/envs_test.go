package envs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const sha = "sha256:"

func hash(c byte) string { return sha + strings.Repeat(string(c), 64) }

func ident() Identity {
	return Identity{OSArch: "darwin-arm64", PythonABI: "cp313", LockDigest: hash('a'), ArtifactsDigest: hash('b')}
}

type fixture struct {
	t    *testing.T
	db   *store.DB
	repo *Repository
	base string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "app.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	base := filepath.Join(dir, "envs")
	if err = os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(base)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, db: db, repo: repo, base: repo.base}
}

// do runs fn in one committed transaction.
func (f *fixture) do(fn func(tx store.Tx) error) error {
	f.t.Helper()
	tx, err := f.db.Begin(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// venv makes a directory with an executable "interpreter" below root.
func (f *fixture) venv(name string) (root string) {
	f.t.Helper()
	root = filepath.Join(f.base, name)
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "python"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		f.t.Fatal(err)
	}
	return root
}

func ok(Launch) error { return nil }

func (f *fixture) ready(name string, id Identity) string {
	f.t.Helper()
	var digest string
	root := f.venv(name)
	if err := f.do(func(tx store.Tx) (err error) {
		if digest, err = f.repo.BeginDeploy(tx, id, root, "bin/python", false); err != nil {
			return err
		}
		state, err := f.repo.Finish(tx, digest, ok)
		if err == nil && state != Ready {
			err = ErrNotReady
		}
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return digest
}

func TestDigestIdentifiesEveryPartOfTheIdentity(t *testing.T) {
	base, err := DigestOf(ident())
	if err != nil || !strings.HasPrefix(base, sha) || len(base) != 71 {
		t.Fatalf("%q %v", base, err)
	}
	if again, _ := DigestOf(ident()); again != base {
		t.Fatal("not deterministic")
	}
	for name, mutate := range map[string]func(*Identity){
		"os/arch":   func(i *Identity) { i.OSArch = "windows-amd64" },
		"abi":       func(i *Identity) { i.PythonABI = "cp314" },
		"lock":      func(i *Identity) { i.LockDigest = hash('c') },
		"artifacts": func(i *Identity) { i.ArtifactsDigest = hash('c') },
	} {
		i := ident()
		mutate(&i)
		if d, _ := DigestOf(i); d == base || d == "" {
			t.Fatalf("%s does not change the digest", name)
		}
	}
	for name, bad := range map[string]Identity{
		"empty":        {},
		"short lock":   {OSArch: "darwin-arm64", PythonABI: "cp313", LockDigest: "sha256:ab", ArtifactsDigest: hash('b')},
		"uppercase":    {OSArch: "darwin-arm64", PythonABI: "cp313", LockDigest: sha + strings.Repeat("A", 64), ArtifactsDigest: hash('b')},
		"bad abi":      {OSArch: "darwin-arm64", PythonABI: "3.13", LockDigest: hash('a'), ArtifactsDigest: hash('b')},
		"bad platform": {OSArch: "Darwin/arm64", PythonABI: "cp313", LockDigest: hash('a'), ArtifactsDigest: hash('b')},
	} {
		if _, err := DigestOf(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestAnEnvironmentIsLaunchableOnlyAfterTheCheckPasses(t *testing.T) {
	f := setup(t)
	root := f.venv("e1")
	scratch := t.TempDir()
	var digest string
	if err := f.do(func(tx store.Tx) (err error) {
		digest, err = f.repo.BeginDeploy(tx, ident(), root, "bin/python", false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	launch := func() error {
		return f.do(func(tx store.Tx) error {
			_, err := f.repo.LaunchSpec(tx, digest, "tacet_format_docx", nil, scratch)
			return err
		})
	}
	if err := launch(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("deploying environment launched: %v", err)
	}
	// a failing probe leaves it Broken (a recorded outcome, committed), and Broken is not launchable
	if err := f.do(func(tx store.Tx) error {
		state, err := f.repo.Finish(tx, digest, func(Launch) error { return errors.New("import failed") })
		if err != nil || state != Broken {
			t.Fatalf("probe failure: %s %v", state, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := launch(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("broken environment launched: %v", err)
	}
	var e Env
	f.do(func(tx store.Tx) (err error) { e, err = f.repo.Get(tx, digest); return err })
	if e.State != Broken {
		t.Fatalf("state %s", e.State)
	}
	// Broken cannot be revived by Finish: it must be redeployed
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Finish(tx, digest, ok); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("finish on a broken environment: %v", err)
	}
}

func TestReadyEnvironmentLaunchSpecIsIsolatedAndHasNoInheritedEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "/tmp/planted")
	t.Setenv("PYTHONHOME", "/tmp/planted")
	t.Setenv("TACET_SECRET_TOKEN", "must-not-leak")
	f := setup(t)
	digest := f.ready("e1", ident())
	scratch := t.TempDir()
	var l Launch
	if err := f.do(func(tx store.Tx) (err error) {
		l, err = f.repo.LaunchSpec(tx, digest, "tacet_format_docx", []string{"--flag"}, scratch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(l.Path) || !strings.HasSuffix(l.Path, filepath.Join("e1", "bin", "python")) || l.Dir != scratch {
		t.Fatalf("path %q dir %q", l.Path, l.Dir)
	}
	if len(l.Args) < 4 || l.Args[0] != "-I" || l.Args[1] != "-m" || l.Args[2] != "tacet_format_docx" || l.Args[3] != "--flag" {
		t.Fatalf("args %v", l.Args)
	}
	for _, e := range l.Env {
		for _, banned := range []string{"PYTHONPATH", "PYTHONHOME", "TACET_SECRET", "HOME="} {
			if strings.HasPrefix(e, banned) {
				t.Fatalf("environment leaks %s", e)
			}
		}
	}
	joined := strings.Join(l.Env, "\n")
	if !strings.Contains(joined, "PYTHONNOUSERSITE=1") || !strings.Contains(joined, "PYTHONDONTWRITEBYTECODE=1") {
		t.Fatalf("env %v", l.Env)
	}
}

func TestLaunchSpecRejectsBadModuleDirectoryAndArguments(t *testing.T) {
	f := setup(t)
	digest := f.ready("e1", ident())
	scratch := t.TempDir()
	try := func(module, dir string, args ...string) error {
		return f.do(func(tx store.Tx) error { _, err := f.repo.LaunchSpec(tx, digest, module, args, dir); return err })
	}
	for _, m := range []string{"", "a b", "a;rm", "../x", "a-b", "pkg.", ".pkg", "pkg/mod", "-c"} {
		if err := try(m, scratch); !errors.Is(err, ErrInvalid) {
			t.Fatalf("module %q accepted: %v", m, err)
		}
	}
	for _, d := range []string{"", "relative", filepath.Join(scratch, "missing")} {
		if err := try("tacet_format_docx", d); !errors.Is(err, ErrInvalid) {
			t.Fatalf("dir %q accepted: %v", d, err)
		}
	}
	if err := try("tacet_format_docx", scratch, "a\x00b"); !errors.Is(err, ErrInvalid) {
		t.Fatal("NUL argument accepted")
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.repo.LaunchSpec(tx, hash('9'), "m", nil, scratch)
		return err
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown digest: %v", err)
	}
}

func TestInterpreterChangedAfterRegistrationIsRefused(t *testing.T) {
	f := setup(t)
	digest := f.ready("e1", ident())
	scratch := t.TempDir()
	interpreter := filepath.Join(f.base, "e1", "bin", "python")
	launch := func() error {
		return f.do(func(tx store.Tx) error { _, err := f.repo.LaunchSpec(tx, digest, "m", nil, scratch); return err })
	}
	if err := launch(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" { // Windows has no execute bit to take away
		os.Chmod(interpreter, 0o600) // no longer executable
		if err := launch(); !errors.Is(err, ErrInterpreter) {
			t.Fatalf("non-executable: %v", err)
		}
	}
	os.Remove(interpreter)
	if err := launch(); !errors.Is(err, ErrInterpreter) {
		t.Fatalf("deleted: %v", err)
	}
	os.Mkdir(interpreter, 0o700)
	if err := launch(); !errors.Is(err, ErrInterpreter) {
		t.Fatalf("replaced by a directory: %v", err)
	}
}

func TestRegistrationRefusesPathsOutsideTheBaseAndRelativeEscapes(t *testing.T) {
	f := setup(t)
	outside := t.TempDir()
	try := func(root, interp string, builtin bool) error {
		return f.do(func(tx store.Tx) error {
			_, err := f.repo.BeginDeploy(tx, ident(), root, interp, builtin)
			return err
		})
	}
	if err := try(outside, "bin/python", false); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("third party outside base: %v", err)
	}
	if err := try(filepath.Join(f.base, "..", "elsewhere"), "bin/python", false); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("dot-dot escape: %v", err)
	}
	if err := try("relative", "bin/python", false); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("relative root: %v", err)
	}
	root := f.venv("e1")
	for _, interp := range []string{"", "/usr/bin/python3", `\bin\python`, "../../bin/sh", "bin/../../x"} {
		if err := try(root, interp, false); !errors.Is(err, ErrInterpreter) {
			t.Fatalf("interpreter %q accepted: %v", interp, err)
		}
	}
	if err := try(outside, "bin/python", true); err != nil { // the signed bundle may live elsewhere
		t.Fatalf("builtin outside base: %v", err)
	}
	if err := try(outside, "bin/python", true); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate registration: %v", err)
	}
}

func TestRedeployMarksOnlyThirdPartyEnvironmentsOfAnotherAbi(t *testing.T) {
	f := setup(t)
	old := f.ready("old", ident())
	newer := ident()
	newer.PythonABI = "cp314"
	newer.ArtifactsDigest = hash('d')
	current := f.ready("current", newer)
	other := ident()
	other.OSArch = "windows-amd64"
	other.ArtifactsDigest = hash('e')
	wrongOS := f.ready("wrongos", other)
	builtinRoot := f.venv("bundle")
	b := ident()
	b.LockDigest = hash('f')
	var builtin string
	f.do(func(tx store.Tx) (err error) {
		builtin, err = f.repo.BeginDeploy(tx, b, builtinRoot, "bin/python", true)
		if err == nil {
			_, err = f.repo.Finish(tx, builtin, ok)
		}
		return err
	})
	var marked []string
	if err := f.do(func(tx store.Tx) (err error) {
		marked, err = f.repo.MarkRedeploy(tx, "darwin-arm64", "cp314")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(marked) != 2 {
		t.Fatalf("marked %v", marked)
	}
	state := func(d string) State {
		var e Env
		f.do(func(tx store.Tx) (err error) { e, err = f.repo.Get(tx, d); return err })
		return e.State
	}
	if state(old) != NeedsRedeploy || state(wrongOS) != NeedsRedeploy {
		t.Fatal("stale third-party environments were not marked")
	}
	if state(current) != Ready || state(builtin) != Ready {
		t.Fatal("a current or builtin environment was marked")
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.LaunchSpec(tx, old, "m", nil, t.TempDir()); return err }); !errors.Is(err, ErrNotReady) {
		t.Fatalf("stale environment launched: %v", err)
	}
	events, _ := f.db.Pending(context.Background(), "q", 10)
	if len(events) != 1 || events[0].Event.Type != "EnvironmentsChanged" {
		t.Fatalf("events %+v", events)
	}
}

func TestBrokenMarkAndBuiltinProtection(t *testing.T) {
	f := setup(t)
	digest := f.ready("e1", ident())
	if err := f.do(func(tx store.Tx) error { return f.repo.MarkBroken(tx, digest) }); err != nil {
		t.Fatal(err)
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.LaunchSpec(tx, digest, "m", nil, t.TempDir()); return err }); !errors.Is(err, ErrNotReady) {
		t.Fatalf("broken launched: %v", err)
	}
	b := ident()
	b.LockDigest = hash('f')
	root := f.venv("bundle")
	var builtin string
	f.do(func(tx store.Tx) (err error) {
		builtin, err = f.repo.BeginDeploy(tx, b, root, "bin/python", true)
		return err
	})
	if err := f.do(func(tx store.Tx) error { return f.repo.MarkBroken(tx, builtin) }); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("builtin marked broken: %v", err)
	}
	if err := f.do(func(tx store.Tx) error { return f.repo.MarkBroken(tx, hash('1')) }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestReclaimKeepsReferencedAndBuiltinEnvironmentsAndOnlyDeletesInsideTheBase(t *testing.T) {
	f := setup(t)
	used := f.ready("used", ident())
	i2 := ident()
	i2.ArtifactsDigest = hash('2')
	unused := f.ready("unused", i2)
	i3 := ident()
	i3.ArtifactsDigest = hash('3')
	f.do(func(tx store.Tx) error { return f.repo.MarkBroken(tx, f.mustDigest(i3, f.venv("half"), tx)) }) // a crashed deployment: broken, unreferenced
	b := ident()
	b.LockDigest = hash('f')
	var builtin string
	f.do(func(tx store.Tx) (err error) {
		builtin, err = f.repo.BeginDeploy(tx, b, f.venv("bundle"), "bin/python", true)
		return err
	})
	var roots []string
	if err := f.do(func(tx store.Tx) (err error) {
		roots, err = f.repo.Reclaim(tx, func(_ store.Tx, d string) (bool, error) { return d == used, nil })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || !strings.HasSuffix(roots[0], "half") || !strings.HasSuffix(roots[1], "unused") {
		t.Fatalf("roots %v", roots)
	}
	for _, d := range []string{used, builtin} {
		if err := f.do(func(tx store.Tx) error { _, err := f.repo.Get(tx, d); return err }); err != nil {
			t.Fatalf("%s was reclaimed", d)
		}
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Get(tx, unused); return err }); !errors.Is(err, ErrNotFound) {
		t.Fatal("unreferenced environment row survived")
	}
	for _, r := range roots {
		if err := f.repo.RemoveRoot(r); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(r); err == nil {
			t.Fatal("directory survived")
		}
	}
	if err := f.repo.RemoveRoot(f.base); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("the base itself: %v", err)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600)
	if err := f.repo.RemoveRoot(outside); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("outside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("a directory outside the base was deleted")
	}
	link := filepath.Join(f.base, "link")
	os.Symlink(outside, link)
	if err := f.repo.RemoveRoot(link); !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("symlink out of the base: %v", err)
	}
	if err := f.repo.RemoveRoot(filepath.Join(f.base, "gone")); err != nil {
		t.Fatalf("already gone must be fine: %v", err)
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Reclaim(tx, nil); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil referenced: %v", err)
	}
}

func (f *fixture) mustDigest(id Identity, root string, tx store.Tx) string {
	d, err := f.repo.BeginDeploy(tx, id, root, "bin/python", false)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func TestAnInterpreterThatIsMissingAtFinishIsBrokenWithoutRunningTheProbe(t *testing.T) {
	f := setup(t)
	root := f.venv("e1")
	os.Remove(filepath.Join(root, "bin", "python"))
	var digest string
	var state State
	probed := false
	if err := f.do(func(tx store.Tx) (err error) {
		if digest, err = f.repo.BeginDeploy(tx, ident(), root, "bin/python", false); err != nil {
			return err
		}
		state, err = f.repo.Finish(tx, digest, func(Launch) error { probed = true; return nil })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if state != Broken || probed {
		t.Fatalf("state %s probed=%v", state, probed)
	}
}

func TestFinishNeedsAProbeAndADeployingEnvironment(t *testing.T) {
	f := setup(t)
	root := f.venv("e1")
	var digest string
	f.do(func(tx store.Tx) (err error) {
		digest, err = f.repo.BeginDeploy(tx, ident(), root, "bin/python", false)
		return err
	})
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Finish(tx, digest, nil); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil probe: %v", err)
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.repo.Finish(tx, hash('7'), ok); return err }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

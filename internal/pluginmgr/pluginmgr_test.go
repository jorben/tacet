package pluginmgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/registry"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const goodLock = `version = 1
[[package]]
name = "demo"
version = "0.1.0"
source = { editable = "." }

[[package]]
name = "dep"
version = "1.0"
source = { registry = "https://pypi.org/simple" }
wheels = [
    { url = "https://files.pythonhosted.org/dep-1.0-py3-none-any.whl", hash = "sha256:00" },
]
`

type rig struct {
	t                           *testing.T
	m                           *Manager
	db                          *store.DB
	reg                         *registry.Repository
	gate                        *netgate.Gate
	dir                         string
	deployed                    []string
	cleaned                     []string
	probes                      int
	deployErr, probeErr, gitErr error
	gitCalls                    []string
	sandbox                     bool
	held                        map[netgate.LeaseKind]uint64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, gate: netgate.NewGate(), dir: t.TempDir(), sandbox: true}
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "p.db")))
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.NewRegistry(registry.Module())
	if err != nil {
		t.Fatal(err)
	}
	r.db, err = store.Open(context.Background(), raw, sr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Close() })
	r.reg, err = registry.NewRepository("darwin-arm64", func() bool { return r.sandbox })
	if err != nil {
		t.Fatal(err)
	}
	r.m, err = New(Config{Dir: r.dir, Gate: r.gate, Registry: r.reg, Commit: r.commit,
		Git: func(_ context.Context, url, commit, dest string) error {
			r.gitCalls = append(r.gitCalls, url+"@"+commit)
			if r.gitErr != nil {
				return r.gitErr
			}
			return writePkg(dest, pluginFiles("acme.git", "1.0.0"))
		},
		Deploy: func(_ context.Context, snap string, m registry.Manifest) (string, error) {
			r.held = r.gate.State()
			r.deployed = append(r.deployed, snap)
			if r.deployErr != nil {
				return "", r.deployErr
			}
			return "sha256:" + strings.Repeat("e", 64), nil
		},
		Cleanup: func(_ context.Context, env string) error { r.cleaned = append(r.cleaned, env); return nil },
		Probe: func(context.Context, string, registry.Manifest, string) error {
			r.probes++
			return r.probeErr
		}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) commit(ctx context.Context, fn func(store.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func manifest(id, version, kind string) []byte {
	m := map[string]any{"id": id, "version": version, "kind": kind, "protocol": map[string]string{"min": "1.0", "max": "1.0"}, "license": "MIT"}
	if kind == "plugin" {
		m["roles"] = []string{"format"}
		m["runtime"] = map[string]string{"kind": "python", "entry": "demo_plugin"}
		m["platforms"] = []string{"darwin-arm64", "windows-amd64"}
		m["formats"] = []string{"docx"}
	}
	b, _ := json.Marshal(m)
	return b
}

func pluginFiles(id, version string) map[string]string {
	return map[string]string{"manifest.json": string(manifest(id, version, "plugin")), "uv.lock": goodLock, "src/demo_plugin/__init__.py": "x = 1\n", "src/demo_plugin/__main__.py": "print('hi')\n"}
}

func writePkg(dir string, files map[string]string) error {
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (r *rig) pkg(files map[string]string) string {
	r.t.Helper()
	d := filepath.Join(r.t.TempDir(), "pkg")
	if err := writePkg(d, files); err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) install(files map[string]string) (registry.Plugin, Inspection) {
	r.t.Helper()
	insp, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(files)})
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := r.m.Confirm(context.Background(), insp.Token)
	if err != nil {
		r.t.Fatal(err)
	}
	return p, insp
}

func (r *rig) stagingEmpty() bool {
	es, _ := os.ReadDir(filepath.Join(r.dir, "staging"))
	return len(es) == 0
}

func (r *rig) get(id registry.Identity) (p registry.Plugin, err error) {
	err = r.commit(context.Background(), func(tx store.Tx) (e error) { p, e = r.reg.Get(tx, id); return })
	return
}

func TestAnInstallInspectsConfirmsDeploysChecksAndRegistersDisabled(t *testing.T) {
	r := newRig(t)
	insp, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))})
	if err != nil {
		t.Fatal(err)
	}
	if insp.ID != "acme.fmt" || insp.Version != "1.0.0" || insp.Kind != "plugin" || insp.License != "MIT" || !insp.NeedsNetwork || insp.Packages != 2 || insp.Origin != "path" ||
		!strings.HasPrefix(insp.Digest, "sha256:") || len(insp.Formats) != 1 || insp.Token == "" {
		t.Fatalf("%+v", insp)
	}
	if len(r.deployed) != 0 {
		t.Fatal("nothing may be deployed before the user confirms")
	}
	p, err := r.m.Confirm(context.Background(), insp.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled || p.Builtin || p.Digest != insp.Digest || p.Environment == "" {
		t.Fatalf("%+v", p)
	}
	if len(r.deployed) != 1 || r.probes != 1 || r.held[netgate.Maintenance] != 1 || r.held[netgate.Network] != 1 {
		t.Fatalf("%v probes=%d held=%v", r.deployed, r.probes, r.held)
	}
	if !strings.HasPrefix(r.deployed[0], filepath.Join(r.dir, "plugins")) {
		t.Fatalf("a plugin runs from its immutable snapshot, not the staging area: %s", r.deployed[0])
	}
	if _, err := os.Stat(filepath.Join(r.deployed[0], "manifest.json")); err != nil || !r.stagingEmpty() {
		t.Fatalf("%v", err)
	}
	if st := r.gate.State(); len(st) != 0 {
		t.Fatalf("leases leaked: %v", st)
	}
	if _, err := r.m.Confirm(context.Background(), insp.Token); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("a token is single use: %v", err)
	}
	var def registry.Plugin
	if err := r.commit(context.Background(), func(tx store.Tx) (e error) { def, e = r.reg.Default(tx, "docx"); return }); !errors.Is(err, registry.ErrMissing) {
		t.Fatalf("installing must not make a default: %+v %v", def, err)
	}
}

func TestAPackageThatNeedsNoDownloadTakesNoNetworkLease(t *testing.T) {
	r := newRig(t)
	files := pluginFiles("acme.fmt", "1.0.0")
	files["uv.lock"] = "[[package]]\nname = \"demo\"\nsource = { virtual = \".\" }\n"
	r.install(files)
	if r.held[netgate.Network] != 0 || r.held[netgate.Maintenance] != 1 {
		t.Fatalf("%v", r.held)
	}
}

func TestEveryFailureRollsTheHalfProductBackAndTouchesNothingElse(t *testing.T) {
	for name, set := range map[string]func(*rig){
		"deployment fails": func(r *rig) { r.deployErr = errors.New("uv failed") },
		"readiness fails":  func(r *rig) { r.probeErr = errors.New("handshake failed") },
		"no sandbox":       func(r *rig) { r.sandbox = false },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			keep, _ := r.installOK()
			set(r)
			insp, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.other", "2.0.0"))})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.m.Confirm(context.Background(), insp.Token); err == nil {
				t.Fatal("accepted")
			}
			es, _ := os.ReadDir(filepath.Join(r.dir, "plugins"))
			if len(es) != 1 {
				t.Fatalf("a failed install left %d snapshot directories", len(es))
			}
			if !r.stagingEmpty() || len(r.gate.State()) != 0 {
				t.Fatal("staging or a lease was left behind")
			}
			if _, err := r.get(registry.Identity{ID: "acme.other", Version: "2.0.0", Digest: insp.Digest}); !errors.Is(err, registry.ErrMissing) {
				t.Fatalf("the failed plugin is registered: %v", err)
			}
			if _, err := r.get(keep.Identity); err != nil {
				t.Fatalf("another plugin was affected: %v", err)
			}
			if name == "readiness fails" && len(r.cleaned) != 1 {
				t.Fatalf("the deployed environment must be removed: %v", r.cleaned)
			}
			if name == "deployment fails" && len(r.cleaned) != 0 {
				t.Fatalf("nothing was deployed: %v", r.cleaned)
			}
		})
	}
}

func (r *rig) installOK() (registry.Plugin, Inspection) {
	return r.install(pluginFiles("acme.keep", "1.0.0"))
}

func TestInstallsDoNotRunWhileDocumentsAreProcessed(t *testing.T) {
	r := newRig(t)
	insp, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))})
	l, _ := r.gate.Acquire(netgate.Processing)
	var busy *netgate.BusyError
	if _, err := r.m.Confirm(context.Background(), insp.Token); !errors.As(err, &busy) {
		t.Fatalf("%v", err)
	}
	if len(r.deployed) != 0 {
		t.Fatal("deployed during processing")
	}
	if _, err := r.m.Prepare(context.Background(), Source{GitURL: "https://x/y.git", Commit: strings.Repeat("a", 40)}); !errors.As(err, &busy) {
		t.Fatalf("fetching during processing: %v", err)
	}
	if len(r.gitCalls) != 0 {
		t.Fatal("git ran during processing")
	}
	l.Release()
}

func TestBuildAndInstallCodeIsRefusedBeforeAnythingIsDeployed(t *testing.T) {
	r := newRig(t)
	for name, edit := range map[string]func(map[string]string){
		"setup.py":       func(f map[string]string) { f["setup.py"] = "import os" },
		"setup.cfg":      func(f map[string]string) { f["setup.cfg"] = "[x]" },
		"install script": func(f map[string]string) { f["scripts/install.sh"] = "rm -rf /" },
		"pth file":       func(f map[string]string) { f["src/evil.pth"] = "import os" },
		"bundled wheel":  func(f map[string]string) { f["vendor/dep.whl"] = "x" },
		"sdist":          func(f map[string]string) { f["vendor/dep.tar.gz"] = "x" },
		"makefile":       func(f map[string]string) { f["Makefile"] = "all:" },
		"lock with an sdist only": func(f map[string]string) {
			f["uv.lock"] = goodLock + "\n[[package]]\nname = \"src\"\nsource = { registry = \"https://pypi.org/simple\" }\nsdist = { url = \"https://x/src.tar.gz\", hash = \"sha256:00\" }\n"
		},
		"lock with a git dependency": func(f map[string]string) {
			f["uv.lock"] = goodLock + "\n[[package]]\nname = \"g\"\nsource = { git = \"https://x/g.git\" }\n"
		},
		"lock with a path dependency": func(f map[string]string) {
			f["uv.lock"] = goodLock + "\n[[package]]\nname = \"g\"\nsource = { directory = \"../g\" }\n"
		},
		"lock with another editable": func(f map[string]string) {
			f["uv.lock"] = goodLock + "\n[[package]]\nname = \"g\"\nsource = { editable = \"../g\" }\n"
		},
		"no lock":      func(f map[string]string) { delete(f, "uv.lock") },
		"empty lock":   func(f map[string]string) { f["uv.lock"] = "version = 1\n" },
		"no manifest":  func(f map[string]string) { delete(f, "manifest.json") },
		"bad manifest": func(f map[string]string) { f["manifest.json"] = "{" },
		"native runtime": func(f map[string]string) {
			f["manifest.json"] = strings.Replace(f["manifest.json"], `"python"`, `"native"`, 1)
		},
		"wrong platform": func(f map[string]string) {
			f["manifest.json"] = strings.Replace(f["manifest.json"], `"darwin-arm64",`, ``, 1)
		},
	} {
		files := pluginFiles("acme.fmt", "1.0.0")
		edit(files)
		if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(files)}); err == nil {
			t.Errorf("%s accepted", name)
		}
		if !r.stagingEmpty() {
			t.Errorf("%s left staging behind", name)
		}
	}
	if len(r.deployed) != 0 {
		t.Fatal("something was deployed")
	}
}

func TestSymbolicLinksAndOversizedTreesAreNotFollowedOrCopied(t *testing.T) {
	r := newRig(t)
	d := r.pkg(pluginFiles("acme.fmt", "1.0.0"))
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("private"), 0o600)
	os.Symlink(secret, filepath.Join(d, "src", "link"))
	if _, err := r.m.Prepare(context.Background(), Source{Path: d}); !errors.Is(err, ErrSource) {
		t.Fatalf("%v", err)
	}
	if !r.stagingEmpty() {
		t.Fatal("staging left")
	}
	if _, err := r.m.Prepare(context.Background(), Source{Path: "relative"}); !errors.Is(err, ErrSource) {
		t.Fatalf("%v", err)
	}
	if _, err := r.m.Prepare(context.Background(), Source{}); !errors.Is(err, ErrSource) {
		t.Fatalf("%v", err)
	}
	if _, err := r.m.Prepare(context.Background(), Source{Path: d, GitURL: "https://x"}); !errors.Is(err, ErrSource) {
		t.Fatalf("both sources: %v", err)
	}
	if _, err := r.m.Prepare(context.Background(), Source{Path: filepath.Join(t.TempDir(), "none")}); err == nil {
		t.Fatal("a missing directory")
	}
}

func TestAGitSourceNeedsAPinnedCommitAndAMatchingDigest(t *testing.T) {
	r := newRig(t)
	commit := strings.Repeat("ab", 20)
	for name, c := range map[string]string{"branch": "main", "short": "abc123", "upper": strings.ToUpper(commit), "empty": ""} {
		if _, err := r.m.Prepare(context.Background(), Source{GitURL: "https://example.com/a.git", Commit: c}); !errors.Is(err, ErrCommit) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(r.gitCalls) != 0 {
		t.Fatal("a moving reference reached git")
	}
	insp, err := r.m.Prepare(context.Background(), Source{GitURL: "https://example.com/a.git", Commit: commit})
	if err != nil || insp.Origin != "git:"+commit || r.gitCalls[0] != "https://example.com/a.git@"+commit {
		t.Fatalf("%+v %v %v", insp, err, r.gitCalls)
	}
	r.m.Cancel(insp.Token)
	if !r.stagingEmpty() {
		t.Fatal("cancel left staging")
	}
	if _, err := r.m.Prepare(context.Background(), Source{GitURL: "https://example.com/a.git", Commit: commit, Digest: "sha256:" + strings.Repeat("0", 64)}); !errors.Is(err, ErrDigest) {
		t.Fatalf("a tampered commit: %v", err)
	}
	if !r.stagingEmpty() {
		t.Fatal("staging left after a digest mismatch")
	}
	insp2, _ := r.m.Prepare(context.Background(), Source{GitURL: "https://example.com/a.git", Commit: commit, Digest: insp.Digest})
	if insp2.Digest != insp.Digest {
		t.Fatal("the same commit must give the same digest")
	}
	r.gitErr = errors.New("network down")
	if _, err := r.m.Prepare(context.Background(), Source{GitURL: "https://example.com/a.git", Commit: commit}); !errors.Is(err, ErrSource) {
		t.Fatalf("%v", err)
	}
	if st := r.gate.State(); len(st) != 0 {
		t.Fatalf("lease leaked: %v", st)
	}
	if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("a.b", "1")), Digest: "md5:x"}); !errors.Is(err, ErrSource) {
		t.Fatalf("%v", err)
	}
}

func TestTheDigestOfALocalPackageIsStableAndContentAddressed(t *testing.T) {
	r := newRig(t)
	a, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))})
	b, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))})
	f := pluginFiles("acme.fmt", "1.0.0")
	f["src/demo_plugin/__init__.py"] = "x = 2\n"
	c, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(f)})
	if a.Digest != b.Digest || a.Digest == c.Digest {
		t.Fatalf("%s %s %s", a.Digest, b.Digest, c.Digest)
	}
	if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0")), Digest: a.Digest}); err != nil {
		t.Fatalf("a matching expected digest: %v", err)
	}
}

func TestInstallingTheSameVersionTwiceIsAConflict(t *testing.T) {
	r := newRig(t)
	r.install(pluginFiles("acme.fmt", "1.0.0"))
	insp, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))})
	if _, err := r.m.Confirm(context.Background(), insp.Token); !errors.Is(err, registry.ErrConflict) {
		t.Fatalf("%v", err)
	}
	if len(r.deployed) != 1 {
		t.Fatal("a duplicate was deployed")
	}
	es, _ := os.ReadDir(filepath.Join(r.dir, "plugins"))
	if len(es) != 1 || !r.stagingEmpty() {
		t.Fatal("the duplicate left files")
	}
}

func rulePack(id string) map[string]string {
	return map[string]string{"manifest.json": string(manifest(id, "1.0.0", "ruleset")),
		"rules.json": `{"id":"` + id + `","rules":[{"id":"badge","category":"staff_id","pattern":"EMP[0-9]{6}","boundary":"digit"}]}`}
}

func TestARulePackIsCompiledAndRegisteredWithoutAnEnvironmentOrAProcess(t *testing.T) {
	r := newRig(t)
	p, insp := r.install(rulePack("acme.rules"))
	if p.Environment != "" || p.Manifest.Kind != "ruleset" || insp.NeedsNetwork || len(r.deployed) != 0 || r.probes != 0 {
		t.Fatalf("%+v deployed=%v probes=%d", p, r.deployed, r.probes)
	}
	r.sandbox = false
	if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(rulePack("acme.rules2"))}); err != nil {
		t.Fatalf("rule packs need no code sandbox: %v", err)
	}
	for name, edit := range map[string]func(map[string]string){
		"no rules file": func(f map[string]string) { delete(f, "rules.json") },
		"invalid json":  func(f map[string]string) { f["rules.json"] = "{" },
		"unknown member": func(f map[string]string) {
			f["rules.json"] = `{"id":"x","rules":[{"id":"a","category":"c","pattern":"a","exec":"rm"}]}`
		},
		"no rules": func(f map[string]string) { f["rules.json"] = `{"id":"x","rules":[]}` },
		"too costly": func(f map[string]string) {
			f["rules.json"] = `{"id":"x","rules":[{"id":"a","category":"c","pattern":"(a+)+$"}]}`
		},
		"bad pattern": func(f map[string]string) {
			f["rules.json"] = `{"id":"x","rules":[{"id":"a","category":"c","pattern":"("}]}`
		},
		"declares code": func(f map[string]string) {
			f["manifest.json"] = strings.Replace(f["manifest.json"], `"kind":"ruleset"`, `"kind":"ruleset","runtime":{"kind":"python","entry":"x"}`, 1)
		},
		"install code": func(f map[string]string) { f["setup.py"] = "x" },
	} {
		files := rulePack("acme.bad")
		edit(files)
		if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(files)}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestUninstallIsBlockedWhileAnythingReferencesTheVersionAndNamesWhat(t *testing.T) {
	r := newRig(t)
	p, _ := r.installOK()
	ctx := context.Background()
	r.commit(ctx, func(tx store.Tx) error {
		return r.reg.Pin(tx, registry.Reference{Workspace: "ws1", Owner: "job-1", Kind: "plugin", Resource: p.ID, Version: p.Version, Digest: p.Digest})
	})
	err := r.m.Uninstall(ctx, p.Identity)
	var re *ReferencedError
	if !errors.As(err, &re) || !errors.Is(err, registry.ErrReferenced) || len(re.Refs) != 1 || re.Refs[0].Owner != "job-1" {
		t.Fatalf("%v", err)
	}
	if _, err := r.get(p.Identity); err != nil {
		t.Fatal("a referenced version was removed")
	}
	if len(r.cleaned) != 0 {
		t.Fatal("its environment was cleaned")
	}
	r.commit(ctx, func(tx store.Tx) error { return r.reg.Unpin(tx, "ws1", "job-1") })
	if err := r.m.Uninstall(ctx, p.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := r.get(p.Identity); !errors.Is(err, registry.ErrMissing) {
		t.Fatalf("%v", err)
	}
	if len(r.cleaned) != 1 {
		t.Fatalf("%v", r.cleaned)
	}
	es, _ := os.ReadDir(filepath.Join(r.dir, "plugins"))
	if len(es) != 0 {
		t.Fatalf("the snapshot was kept: %v", es)
	}
	if err := r.m.Uninstall(ctx, p.Identity); !errors.Is(err, registry.ErrMissing) {
		t.Fatalf("%v", err)
	}
}

func TestBuiltinPluginsCannotBeDisabledOrUninstalledHere(t *testing.T) {
	r := newRig(t)
	var b registry.Plugin
	r.commit(context.Background(), func(tx store.Tx) (err error) {
		b, err = r.reg.Register(tx, manifest("tacet.builtin", "1.0.0", "plugin"), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("e", 64), true)
		return
	})
	if _, err := r.m.Disable(context.Background(), b.Identity); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("%v", err)
	}
	if err := r.m.Uninstall(context.Background(), b.Identity); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("%v", err)
	}
}

func TestDisableReportsWhatStillUsesTheVersionAndNeverChangesIt(t *testing.T) {
	r := newRig(t)
	p, _ := r.installOK()
	ctx := context.Background()
	if err := r.m.Enable(ctx, p.Identity); err != nil {
		t.Fatal(err)
	}
	r.commit(ctx, func(tx store.Tx) error {
		return r.reg.Pin(tx, registry.Reference{Workspace: "ws1", Owner: "job-1", Kind: "plugin", Resource: p.ID, Version: p.Version, Digest: p.Digest})
	})
	refs, err := r.m.Disable(ctx, p.Identity)
	if err != nil || len(refs) != 1 {
		t.Fatalf("%v %v", refs, err)
	}
	if got, _ := r.get(p.Identity); got.Enabled {
		t.Fatal("still enabled")
	}
	r.sandbox = false
	if err := r.m.Enable(ctx, p.Identity); !errors.Is(err, registry.ErrSandbox) {
		t.Fatalf("a platform without a passed sandbox does not enable third-party code: %v", err)
	}
}

func TestAnUpgradeKeepsTheOldVersionForRollbackAndGrantsAreNotInherited(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v1, _ := r.install(pluginFiles("acme.fmt", "1.0.0"))
	f := pluginFiles("acme.fmt", "1.1.0")
	f["src/demo_plugin/__init__.py"] = "x = 2\n"
	v2, _ := r.install(f)
	r.m.Enable(ctx, v1.Identity)
	r.commit(ctx, func(tx store.Tx) error { return r.reg.GrantCompletion(tx, v1.Identity) })
	if err := r.m.Rollback(ctx, v1.Identity, v2.Identity); err != nil {
		t.Fatal(err)
	}
	a, _ := r.get(v1.Identity)
	b, _ := r.get(v2.Identity)
	if a.Enabled || !b.Enabled {
		t.Fatalf("v1=%v v2=%v", a.Enabled, b.Enabled)
	}
	var g1, g2 bool
	r.commit(ctx, func(tx store.Tx) (err error) {
		if g1, err = r.reg.CompletionGranted(tx, v1.Identity); err != nil {
			return err
		}
		g2, err = r.reg.CompletionGranted(tx, v2.Identity)
		return err
	})
	if !g1 || g2 {
		t.Fatalf("a grant must stay with the version it was given to: v1=%v v2=%v", g1, g2)
	}
	if err := r.m.Rollback(ctx, v2.Identity, v1.Identity); err != nil {
		t.Fatal(err)
	}
	if a, _ = r.get(v1.Identity); !a.Enabled {
		t.Fatal("rolled back")
	}
	if err := r.m.Rollback(ctx, v1.Identity, registry.Identity{ID: "acme.fmt", Version: "0.9", Digest: v1.Digest}); !errors.Is(err, ErrRollback) {
		t.Fatalf("a version that is not installed: %v", err)
	}
	if err := r.m.Rollback(ctx, v1.Identity, v1.Identity); !errors.Is(err, ErrRollback) {
		t.Fatalf("%v", err)
	}
	if err := r.m.Rollback(ctx, v1.Identity, registry.Identity{ID: "other.id", Version: "1", Digest: v1.Digest}); !errors.Is(err, ErrRollback) {
		t.Fatalf("%v", err)
	}
}

func TestConfigurationMustBeCompleteAndAbsolute(t *testing.T) {
	r := newRig(t)
	ok := r.m.cfg
	edit := func(f func(*Config)) Config { c := ok; f(&c); return c }
	for name, c := range map[string]Config{"relative": edit(func(c *Config) { c.Dir = "x" }), "no gate": edit(func(c *Config) { c.Gate = nil }), "no registry": edit(func(c *Config) { c.Registry = nil }),
		"no commit": edit(func(c *Config) { c.Commit = nil }), "no deploy": edit(func(c *Config) { c.Deploy = nil }), "no cleanup": edit(func(c *Config) { c.Cleanup = nil }), "no probe": edit(func(c *Config) { c.Probe = nil })} {
		if _, err := New(c); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.m.Confirm(context.Background(), "nope"); !errors.Is(err, ErrNoCandidate) {
		t.Fatal(err)
	}
	r.m.Cancel("nope")
}

func TestPackageSizeAndFileCountAreBounded(t *testing.T) {
	r := newRig(t)
	oldF, oldT := maxFiles, maxTotal
	defer func() { maxFiles, maxTotal = oldF, oldT }()
	maxFiles = 3
	if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too many files: %v", err)
	}
	maxFiles, maxTotal = oldF, 20
	if _, err := r.m.Prepare(context.Background(), Source{Path: r.pkg(pluginFiles("acme.fmt", "1.0.0"))}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too many bytes: %v", err)
	}
	if !r.stagingEmpty() {
		t.Fatal("staging left")
	}
}

func TestTheDigestCoversFileNamesNotOnlyContent(t *testing.T) {
	r := newRig(t)
	f := pluginFiles("acme.fmt", "1.0.0")
	a, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(f)})
	f["src/demo_plugin/__inif__.py"] = f["src/demo_plugin/__init__.py"]
	delete(f, "src/demo_plugin/__init__.py")
	b, _ := r.m.Prepare(context.Background(), Source{Path: r.pkg(f)})
	if a.Digest == b.Digest {
		t.Fatal("renaming a file must change the digest")
	}
}

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func digest(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }
func fixture(t *testing.T, sandbox func() bool) (*store.DB, *Repository) {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "catalog.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r, err := NewRepository("darwin-arm64", sandbox)
	if err != nil {
		t.Fatal(err)
	}
	return db, r
}
func commit(t *testing.T, db *store.DB, fn func(store.Tx) error) error {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func manifest(t *testing.T, id, version, runtime string) []byte {
	t.Helper()
	value := map[string]any{"id": id, "version": version, "kind": "plugin", "protocol": map[string]string{"min": "1.0", "max": "1.0"}, "license": "MIT", "roles": []string{"format"}, "runtime": map[string]string{"kind": runtime, "entry": "example_plugin"}, "platforms": []string{"darwin-arm64", "windows-amd64"}, "formats": []string{"docx"}, "capabilities": map[string]any{"inventory": map[string]bool{"supported": true}}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func install(t *testing.T, db *store.DB, r *Repository, id, version, d string, builtin bool) Plugin {
	t.Helper()
	var p Plugin
	if err := commit(t, db, func(tx store.Tx) error {
		var err error
		p, err = r.Register(tx, manifest(t, id, version, "python"), digest(d), digest("e"), builtin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegistryDefaultNeverChangesOnInstallOrEnable(t *testing.T) {
	db, r := fixture(t, func() bool { return true })
	builtin := install(t, db, r, "tacet.docx", "1.0", "a", true)
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.Enable(tx, builtin.Identity, true); err != nil {
			return err
		}
		return r.SetDefault(tx, "docx", builtin.Identity)
	}); err != nil {
		t.Fatal(err)
	}
	third := install(t, db, r, "third.docx", "1.0", "b", false)
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.Enable(tx, third.Identity, true); err != nil {
			return err
		}
		selected, err := r.Default(tx, "docx")
		if err != nil {
			return err
		}
		if selected.Identity != builtin.Identity {
			t.Fatal("new plugin silently replaced default")
		}
		return r.SetDefault(tx, "docx", third.Identity)
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		selected, err := r.Default(tx, "docx")
		if err != nil {
			return err
		}
		if selected.Identity != third.Identity {
			t.Fatal("explicit default selection not persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryGrantsBindVersionAndDigest(t *testing.T) {
	db, r := fixture(t, func() bool { return true })
	one := install(t, db, r, "third.docx", "1.0", "a", false)
	two := install(t, db, r, "third.docx", "2.0", "b", false)
	repacked := install(t, db, r, "third.docx", "1.0", "c", false)
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.GrantCompletion(tx, one.Identity); err != nil {
			return err
		}
		for _, p := range []Plugin{one, two, repacked} {
			granted, err := r.CompletionGranted(tx, p.Identity)
			if err != nil {
				return err
			}
			if granted != (p.Identity == one.Identity) {
				t.Fatal("grant inherited by another version or digest")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryReferenceProtectionSpansWorkspacesAndDisable(t *testing.T) {
	db, r := fixture(t, func() bool { return true })
	p := install(t, db, r, "third.docx", "1.0", "a", false)
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.Enable(tx, p.Identity, true); err != nil {
			return err
		}
		for _, workspace := range []string{"gui", "cli"} {
			if err := r.Pin(tx, Reference{Workspace: workspace, Owner: "job", Kind: "plugin", Resource: p.ID, Version: p.Version, Digest: p.Digest}); err != nil {
				return err
			}
		}
		return r.Enable(tx, p.Identity, false)
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Remove(tx, p.Identity) }); !errors.Is(err, ErrReferenced) {
		t.Fatal("disabled referenced version removed")
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Unpin(tx, "gui", "job") }); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Remove(tx, p.Identity) }); !errors.Is(err, ErrReferenced) {
		t.Fatal("CLI reference missed")
	}
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.Unpin(tx, "cli", "job"); err != nil {
			return err
		}
		return r.Remove(tx, p.Identity)
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { _, err := r.Get(tx, p.Identity); return err }); !errors.Is(err, ErrMissing) {
		t.Fatal("unreferenced removal failed")
	}
}

func TestRegistrySandboxGateAndRuntimeFailClosed(t *testing.T) {
	accepted := true
	db, r := fixture(t, func() bool { return accepted })
	third := install(t, db, r, "third.docx", "1.0", "a", false)
	accepted = false
	if err := commit(t, db, func(tx store.Tx) error { return r.Enable(tx, third.Identity, true) }); !errors.Is(err, ErrSandbox) {
		t.Fatal("third-party code enabled without sandbox")
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Register(tx, manifest(t, "new.docx", "1.0", "python"), digest("b"), digest("e"), false)
		return err
	}); !errors.Is(err, ErrSandbox) {
		t.Fatal("third-party install allowed without sandbox")
	}
	builtin := install(t, db, r, "tacet.docx", "1.0", "c", true)
	if err := commit(t, db, func(tx store.Tx) error { return r.Enable(tx, builtin.Identity, true) }); err != nil {
		t.Fatal("builtin degraded mode unavailable")
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Register(tx, manifest(t, "new.runtime", "1.0", "node"), digest("b"), digest("e"), false)
		return err
	}); !errors.Is(err, ErrRuntime) {
		t.Fatal("unsupported runtime accepted")
	}
	if err := commit(t, db, func(tx store.Tx) error {
		data := []byte(`{"id":"third.rules","version":"1.0","kind":"ruleset","license":"MIT","protocol":{"min":"1.0","max":"1.0"}}`)
		p, err := r.Register(tx, data, digest("d"), "", false)
		if err != nil {
			return err
		}
		return r.Enable(tx, p.Identity, true)
	}); err != nil {
		t.Fatalf("declarative ruleset incorrectly needs code sandbox: %v", err)
	}
}

func TestRegistryRollbackDoesNotLeaveCatalogOrGrant(t *testing.T) {
	db, r := fixture(t, func() bool { return true })
	p := install(t, db, r, "third.docx", "1.0", "a", false)
	abort := errors.New("fixture rollback")
	if err := commit(t, db, func(tx store.Tx) error {
		if err := r.GrantCompletion(tx, p.Identity); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		granted, err := r.CompletionGranted(tx, p.Identity)
		if granted {
			t.Fatal("rolled-back grant survived")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReferencesAndVersionsListWhatHoldsAndWhatIsInstalled(t *testing.T) {
	db, repo := fixture(t, func() bool { return true })
	id1, id2 := Identity{"acme.fmt", "1.0.0", digest("a")}, Identity{"acme.fmt", "1.1.0", digest("b")}
	if err := commit(t, db, func(tx store.Tx) error {
		for _, id := range []Identity{id1, id2} {
			if _, err := repo.Register(tx, manifest(t, id.ID, id.Version, "python"), id.Digest, digest("e"), false); err != nil {
				return err
			}
		}
		for _, ref := range []Reference{{"ws2", "task-b", "plugin", id1.ID, id1.Version, id1.Digest}, {"ws1", "task-a", "plugin", id1.ID, id1.Version, id1.Digest},
			{"ws1", "task-c", "plugin", id2.ID, id2.Version, id2.Digest}} {
			if err := repo.Pin(tx, ref); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var refs []Reference
	var vs []Plugin
	commit(t, db, func(tx store.Tx) (err error) {
		if refs, err = repo.References(tx, id1); err != nil {
			return err
		}
		vs, err = repo.Versions(tx, "acme.fmt")
		return err
	})
	if len(refs) != 2 || refs[0].Workspace != "ws1" || refs[1].Workspace != "ws2" {
		t.Fatalf("%+v", refs)
	}
	if len(vs) != 2 || vs[0].Version != "1.0.0" || vs[1].Version != "1.1.0" {
		t.Fatalf("%+v", vs)
	}
	commit(t, db, func(tx store.Tx) error {
		if r, _ := repo.References(tx, Identity{"acme.fmt", "9", digest("z")}); len(r) != 0 {
			t.Fatal("a reference of another version")
		}
		if v, _ := repo.Versions(tx, "nobody"); len(v) != 0 {
			t.Fatal("versions of an unknown id")
		}
		return nil
	})
}

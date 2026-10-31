//go:build darwin || windows

package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/store"
)

type userKey struct {
	key   []byte
	saves int
}

func (u *userKey) Load() ([]byte, error) {
	if u.key == nil {
		return nil, keys.ErrKeyNotFound
	}
	return append([]byte(nil), u.key...), nil
}
func (u *userKey) Save(k []byte) error { u.saves++; u.key = append([]byte(nil), k...); return nil }
func (u *userKey) Delete() error       { u.key = nil; return nil }

const secretMarker = "TEST_SECRET_MARK_WS"

func registry(t *testing.T, extra ...string) *store.Registry {
	t.Helper()
	m := store.Module{Name: "jobs", Prefix: "jobs_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		"CREATE TABLE jobs_admission(id TEXT PRIMARY KEY, attempt TEXT, version INTEGER, state TEXT)"}}}}
	if len(extra) > 0 {
		m.Migrations = append(m.Migrations, store.Migration{Version: 2, SQL: extra})
	}
	r, err := store.NewRegistry(m)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func tctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func put(t *testing.T, w *Workspace, id string) {
	t.Helper()
	if err := w.Commit(tctx(t), func(tx store.Tx) error {
		c, err := store.InTransaction(tx)
		if err != nil {
			return err
		}
		scope, err := c.Module("jobs")
		if err != nil {
			return err
		}
		_, err = scope.Exec("INSERT INTO jobs_admission VALUES(?,?,?,?)", id, "a1", 1, secretMarker)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, w *Workspace) (n int) {
	t.Helper()
	if err := w.Commit(tctx(t), func(tx store.Tx) error {
		c, err := store.InTransaction(tx)
		if err != nil {
			return err
		}
		return c.QueryRow("SELECT count(*) FROM jobs_admission").Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func openManaged(t *testing.T, dir string, u keys.UserKeyStore, create bool, r *store.Registry) (*Workspace, error) {
	return OpenManaged(tctx(t), dir, Managed{UserKey: u, Registry: r, Versions: sqlVersions{}, Create: create})
}

func TestManagedWorkspaceCreatesEncryptedDataAndReopensWithItsKeys(t *testing.T) {
	dir, user := t.TempDir(), &userKey{}
	w, err := openManaged(t, dir, user, true, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	first := w.Generation()
	put(t, w, "j1")
	attempt := kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: first, JobVersion: 1}
	if err = w.Admit(attempt); err != nil {
		t.Fatalf("admission through the encrypted database: %v", err)
	}
	task, err := w.Keys().Create(keys.Task, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if bytes.Contains(data, []byte(secretMarker)) || bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
			t.Fatalf("plaintext in %s", e.Name())
		}
	}
	w, err = openManaged(t, dir, user, false, registry(t))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w.Close()
	if w.Generation() != first+1 || count(t, w) != 1 {
		t.Fatalf("generation %d rows %d", w.Generation(), count(t, w))
	}
	if err = w.Admit(attempt); !errors.Is(err, ErrStale) {
		t.Fatalf("a result of the previous owner generation was accepted: %v", err)
	}
	if again, err := w.Keys().Get(keys.Task, "t1"); err != nil || !bytes.Equal(again, task) {
		t.Fatalf("task key after reopen: %v", err)
	}
}

func TestManagedOpenNeverInventsAWorkspaceOrAKey(t *testing.T) {
	dir, user := t.TempDir(), &userKey{}
	if _, err := openManaged(t, dir, user, false, registry(t)); !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("absent workspace without Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, databaseName)); err == nil || user.saves != 0 {
		t.Fatal("a refused open created a database or a user key")
	}
	w, err := openManaged(t, dir, user, true, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	put(t, w, "j1")
	w.Close()

	lost := &userKey{} // the Keychain entry is gone
	if _, err = openManaged(t, dir, lost, true, registry(t)); !errors.Is(err, ErrKeyMissing) || lost.saves != 0 {
		t.Fatalf("lost user key must not be replaced even with Create: %v saves=%d", err, lost.saves)
	}
	wrapped := filepath.Join(dir, keysDirName, "workspace.workspace.key")
	if err = os.Remove(wrapped); err != nil {
		t.Fatal(err)
	}
	if _, err = openManaged(t, dir, user, true, registry(t)); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("lost wrapped workspace key: %v", err)
	}
	if _, err = os.Stat(wrapped); err == nil {
		t.Fatal("a new workspace key was created beside existing data")
	}
}

func TestManagedSecondOpenIsBusyAndFailedOpenReleasesEverything(t *testing.T) {
	dir, user := t.TempDir(), &userKey{}
	w, err := openManaged(t, dir, user, true, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openManaged(t, dir, user, false, registry(t)); !errors.Is(err, ErrBusy) {
		t.Fatalf("second owner: %v", err)
	}
	put(t, w, "j1")
	w.Close()
	// a database newer than this build is refused, and the refusal frees the lock
	newer, err := openManaged(t, dir, user, false, registry(t, "CREATE TABLE jobs_extra(id INTEGER)"))
	if err != nil {
		t.Fatal(err)
	}
	newer.Close()
	if _, err = openManaged(t, dir, user, false, registry(t)); !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("older build on a newer database: %v", err)
	}
	again, err := openManaged(t, dir, user, false, registry(t, "CREATE TABLE jobs_extra(id INTEGER)"))
	if err != nil {
		t.Fatalf("lock or handle leaked by a failed open: %v", err)
	}
	defer again.Close()
	if count(t, again) != 1 {
		t.Fatal("data changed by a refused open")
	}
}

func TestManagedOpenSettlesABackupLeftByACrashedMigration(t *testing.T) {
	dir, user := t.TempDir(), &userKey{}
	w, err := openManaged(t, dir, user, true, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	put(t, w, "j1")
	key, err := w.Keys().Get(keys.Workspace, workspaceKeyID)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	path := filepath.Join(dir, databaseName)
	raw, err := store.OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.FileBackup(raw, path, key)(tctx(t)); err != nil { // crash right after the backup
		t.Fatal(err)
	}
	raw.Close()
	if err = os.WriteFile(path, []byte("the crash left a broken main file"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err = openManaged(t, dir, user, false, registry(t, "CREATE TABLE jobs_extra(id INTEGER)"))
	if err != nil {
		t.Fatalf("open after a crash: %v", err)
	}
	defer w.Close()
	if count(t, w) != 1 {
		t.Fatal("data not restored from the verified backup")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*premigrate*")); len(left) != 0 {
		t.Fatalf("backup files left: %v", left)
	}
}

func TestManagedRequiresItsInputs(t *testing.T) {
	if _, err := OpenManaged(tctx(t), t.TempDir(), Managed{Registry: registry(t)}); !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("no user key store: %v", err)
	}
	if _, err := OpenManaged(tctx(t), t.TempDir(), Managed{UserKey: &userKey{}}); !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("no registry: %v", err)
	}
	if _, err := OpenManaged(tctx(t), filepath.Join(t.TempDir(), "missing"), Managed{UserKey: &userKey{}, Registry: registry(t), Create: true}); !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("missing directory: %v", err)
	}
}

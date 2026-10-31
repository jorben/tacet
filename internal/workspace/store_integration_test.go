//go:build darwin || windows

package workspace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type sqlVersions struct{}

func (sqlVersions) Current(tx store.Tx, id string) (string, uint64, bool) {
	concrete, err := store.InTransaction(tx)
	if err != nil {
		return "", 0, false
	}
	var attempt string
	var version uint64
	err = concrete.QueryRow("SELECT attempt, version FROM jobs_admission WHERE id=?", id).Scan(&attempt, &version)
	return attempt, version, err == nil
}

func TestStoreAdmissionAndOutcomeCommitShareOneTransaction(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := store.NewRegistry(store.Module{Name: "jobs", Prefix: "jobs_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		"CREATE TABLE jobs_admission(id TEXT PRIMARY KEY, attempt TEXT, version INTEGER, state TEXT)",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, raw, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(dir, BackendFunc(func(ctx context.Context) (Txn, error) { return db.Begin(ctx) }), sqlVersions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = w.Commit(ctx, func(tx store.Tx) error {
		concrete, err := store.InTransaction(tx)
		if err != nil {
			return err
		}
		scope, err := concrete.Module("jobs")
		if err != nil {
			return err
		}
		_, err = scope.Exec("INSERT INTO jobs_admission VALUES(?,?,?,?)", "j1", "a1", 1, "running")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	attempt := kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: w.Generation(), JobVersion: 1}
	if err = w.Commit(ctx, func(tx store.Tx) error {
		if err := w.AdmitTx(tx, attempt); err != nil {
			return err
		}
		concrete, err := store.InTransaction(tx)
		if err != nil {
			return err
		}
		scope, _ := concrete.Module("jobs")
		_, err = scope.Exec("UPDATE jobs_admission SET version=2, state='done' WHERE id='j1'")
		if err != nil {
			return err
		}
		tx.Emit(store.Event{Type: "JobStateChanged", Subject: kernel.ArtifactRef{ID: "j1", Version: 2}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = w.Admit(attempt); !errors.Is(err, ErrStale) {
		t.Fatalf("old version accepted: %v", err)
	}
	events, err := db.Pending(ctx, "query", 10)
	if err != nil || len(events) != 1 || events[0].Event.Subject.Version != 2 {
		t.Fatalf("atomic outbox: %+v %v", events, err)
	}
	// A post-commit action can perform another commit without recursively taking
	// an already-held workspace lock or keeping the store connection occupied.
	var nestedErr error
	if err = w.Commit(ctx, func(tx store.Tx) error {
		tx.AfterCommit(func() { nestedErr = w.Commit(ctx, func(store.Tx) error { return nil }) })
		return nil
	}); err != nil || nestedErr != nil {
		t.Fatalf("nested dispatch: %v %v", err, nestedErr)
	}
	if err = w.Commit(ctx, func(tx store.Tx) error {
		stale := attempt
		stale.AttemptID = "old"
		return w.AdmitTx(tx, stale)
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("late result: %v", err)
	}
	if err = w.AdmitTx(nil, attempt); !errors.Is(err, ErrStale) {
		t.Fatal("admission outside the owned transaction")
	}
}

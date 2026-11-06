//go:build darwin || windows

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

func TestSnapshotFreezeAndBatchConfirmCommitTogether(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module(), snapshot.Module())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs := NewRepository(nil, DefaultLimits())
	ws, err := workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), jobs)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	commit := func(fn func(store.Tx) error) error { return ws.Commit(ctx, fn) }
	if err = commit(func(tx store.Tx) error {
		if err := jobs.CreateDraft(tx, "b", RegularBatch); err != nil {
			return err
		}
		return jobs.AddMember(tx, "b", "j", 0)
	}); err != nil {
		t.Fatal(err)
	}
	var attempt kernel.AttemptRef
	if err = commit(func(tx store.Tx) error {
		var err error
		attempt, err = jobs.BeginStage(tx, "j", kernel.StagePreflight, 1, ws.Generation())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error {
		_, err := jobs.CompleteStage(tx, attempt, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	frozen, err := snapshot.Freeze(snapshot.Content{Detect: snapshot.DetectContent{RuleSetDigest: "rules-v1", RulesOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	repo := snapshot.Repository{}
	rollback := errors.New("fixture authorization failure")
	if err = commit(func(tx store.Tx) error {
		if err := repo.Save(tx, "b", 0, frozen); err != nil {
			return err
		}
		if err := jobs.BindSnapshot(tx, "b", 0, frozen.Version()); err != nil {
			return err
		}
		if _, err := jobs.ConfirmBatch(tx, "b"); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error {
		if _, err := repo.Load(tx, "b", 1); !errors.Is(err, snapshot.ErrSnapshotMissing) {
			t.Fatal("rolled back snapshot exists")
		}
		if _, err := jobs.SnapshotVersion(tx, "b"); !errors.Is(err, ErrJobRecord) {
			t.Fatal("rolled back snapshot binding exists")
		}
		job, err := jobs.Get(tx, "j")
		if err != nil {
			return err
		}
		if job.Position.State != Preflighted {
			t.Fatal("half-confirmed member persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error {
		if err := repo.Save(tx, "b", 0, frozen); err != nil {
			return err
		}
		if err := jobs.BindSnapshot(tx, "b", 0, frozen.Version()); err != nil {
			return err
		}
		_, err := jobs.ConfirmBatch(tx, "b")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	changed := frozen.Content()
	changed.Dispose.MinConfidence = 0.85
	next, _, err := frozen.NewVersion(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error { return repo.Save(tx, "b", 1, next) }); err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error {
		old, err := repo.Load(tx, "b", 1)
		if err != nil {
			return err
		}
		latest, err := repo.Load(tx, "b", 0)
		if err != nil {
			return err
		}
		if old.Content().Dispose.MinConfidence != 0 || latest.Content().Dispose.MinConfidence != 0.85 {
			t.Fatal("new snapshot overwrote frozen content")
		}
		binding, err := jobs.SnapshotVersion(tx, "b")
		if err != nil {
			return err
		}
		if binding != 1 {
			t.Fatal("new snapshot silently rebound running batch")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error { return repo.Save(tx, "b", 0, frozen) }); !errors.Is(err, snapshot.ErrSnapshotConflict) {
		t.Fatalf("stale snapshot write: %v", err)
	}
}

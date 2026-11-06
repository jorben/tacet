//go:build darwin || windows

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/query"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

// Production wiring supplies this adapter in app; jobs does not import query.
type jobViews struct {
	repo   *Repository
	fail   bool
	failID string
}

func (a *jobViews) View(tx store.Tx, id string) (query.JobView, error) {
	if a.fail || a.failID == id {
		return query.JobView{}, errors.New("fixture view unavailable")
	}
	job, err := a.repo.Get(tx, id)
	return query.JobView{ID: job.ID, Batch: job.Batch, Version: job.Version, State: string(job.Position.State), Ordinal: job.Ordinal}, err
}

func TestQueryProjectionAndCursorRollbackTogether(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module(), query.Module())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(nil, DefaultLimits())
	ws, err := workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err = ws.Commit(ctx, func(tx store.Tx) error {
		if err := repo.CreateDraft(tx, "b", RegularBatch); err != nil {
			return err
		}
		if err := repo.AddMember(tx, "b", "j1", 0); err != nil {
			return err
		}
		return repo.AddMember(tx, "b", "j2", 1)
	}); err != nil {
		t.Fatal(err)
	}
	views := &jobViews{repo: repo, failID: "j2"}
	projection := query.NewProjection(db, views)
	if _, err = projection.Pump(ctx, 100); err == nil {
		t.Fatal("failed view was acknowledged")
	}
	rolledBack, err := projection.Counts(ctx, "b")
	if err != nil || len(rolledBack) != 0 {
		t.Fatalf("partially projected state survived rollback: %v %v", rolledBack, err)
	}
	views.failID = ""
	if n, err := projection.Pump(ctx, 100); err != nil || n != 3 {
		t.Fatalf("replayed events=%d err=%v", n, err)
	}
	counts, err := projection.Counts(ctx, "b")
	if err != nil || counts["preflighting"] != 2 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	if n, err := projection.Pump(ctx, 100); err != nil || n != 0 {
		t.Fatalf("double-consume=%d %v", n, err)
	}
	if err = ws.Commit(ctx, func(tx store.Tx) error {
		a, err := repo.BeginStage(tx, "j1", kernel.StagePreflight, 1, ws.Generation())
		if err != nil {
			return err
		}
		_, err = repo.CompleteStage(tx, a, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Only one event per pump. The first event queries the current owner state;
	// the second event sees its version already projected and does not recount.
	for i := 0; i < 2; i++ {
		if _, err = projection.Pump(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	counts, err = projection.Counts(ctx, "b")
	if err != nil || counts["preflighting"] != 1 || counts["preflighted"] != 1 {
		t.Fatalf("incremental counts=%v %v", counts, err)
	}
	first, err := projection.Members(ctx, "b", -1, "", 1)
	if err != nil || len(first) != 1 || first[0].ID != "j1" {
		t.Fatalf("first page: %v %v", first, err)
	}
	second, err := projection.Members(ctx, "b", first[0].Ordinal, first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID != "j2" {
		t.Fatalf("second page: %v %v", second, err)
	}
	if _, err = projection.Members(ctx, "b", -1, "", 201); !errors.Is(err, query.ErrProjection) {
		t.Fatal("unbounded page allowed")
	}
}

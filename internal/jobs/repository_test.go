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
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type fixture struct {
	ctx        context.Context
	db         *store.DB
	ws         *workspace.Workspace
	repo       *Repository
	dispatched []kernel.StageKind
	dir        string
}

func fixtureDB(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	// No wall-clock deadline: a test's commits took over 10s on a loaded Windows
	// runner (run 41391573277). A hang is caught by go test's -timeout instead.
	ctx := t.Context()
	f.ctx = ctx
	dir := t.TempDir()
	f.dir = dir
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	f.db, err = store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.repo = NewRepository(func(_ string, stage kernel.StageKind) { f.dispatched = append(f.dispatched, stage) }, DefaultLimits())
	f.ws, err = workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return f.db.Begin(ctx) }), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.ws.Close() })
	return f
}

func (f *fixture) commit(fn func(store.Tx) error) error { return f.ws.Commit(f.ctx, fn) }
func (f *fixture) create(t *testing.T, id string, kind BatchKind) {
	t.Helper()
	if err := f.commit(func(tx store.Tx) error { return f.repo.CreateDraft(tx, id, kind) }); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) member(t *testing.T, batch, id string) {
	t.Helper()
	if err := f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, batch, id, 0) }); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) get(t *testing.T, id string) Job {
	t.Helper()
	var job Job
	if err := f.commit(func(tx store.Tx) error { var err error; job, err = f.repo.Get(tx, id); return err }); err != nil {
		t.Fatal(err)
	}
	return job
}

// due is the stage the file is at, which is what a current notification names.
func (f *fixture) due(t *testing.T, job Job) kernel.StageKind {
	t.Helper()
	_, stage, ok := beginning(job.Position, f.repo.limits)
	if !ok {
		t.Fatalf("%s has no stage to begin at %s", job.ID, job.Position.State)
	}
	return stage
}
func (f *fixture) start(t *testing.T, id string) kernel.AttemptRef {
	t.Helper()
	job := f.get(t, id)
	var attempt kernel.AttemptRef
	if err := f.commit(func(tx store.Tx) error {
		var err error
		attempt, err = f.repo.BeginStage(tx, id, f.due(t, job), job.Version, f.ws.Generation())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return attempt
}
func (f *fixture) complete(t *testing.T, a kernel.AttemptRef, out sched.StageOutcome) Job {
	t.Helper()
	var job Job
	if err := f.commit(func(tx store.Tx) error {
		if err := f.ws.AdmitTx(tx, a); err != nil {
			return err
		}
		var err error
		job, err = f.repo.CompleteStage(tx, a, out)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestRepositoryCoexistsQuickAndRegularWithoutReplacing(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "quick", QuickBatch)
	f.create(t, "regular", RegularBatch)
	f.create(t, "quick2", QuickBatch) // beside the other of its kind: it replaces nothing
	f.create(t, "regular2", RegularBatch)
	f.member(t, "quick", "jq")
	f.member(t, "regular", "jr")
	f.member(t, "quick2", "jq2")
	if f.get(t, "jq").Batch != "quick" || f.get(t, "jr").Batch != "regular" || f.get(t, "jq2").Batch != "quick2" {
		t.Fatal("draft members changed")
	}
}

func TestRepositoryPersistsOutcomesOnceAndRejectsReplay(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	if len(f.dispatched) != 1 || f.dispatched[0] != kernel.StagePreflight {
		t.Fatal("draft stage not dispatched after commit")
	}
	a := f.start(t, "j")
	job := f.complete(t, a, sched.StageOutcome{Kind: sched.Completed, Artifacts: []kernel.ArtifactRef{{Kind: "preflight", ID: "p", Version: 1}}})
	if job.Position.State != Preflighted || job.AttemptID != "" {
		t.Fatalf("outcome: %+v", job)
	}
	before, err := f.db.Pending(f.ctx, "query", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.commit(func(tx store.Tx) error {
		_, err := f.repo.CompleteStage(tx, a, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("duplicate result: %v", err)
	}
	after, _ := f.db.Pending(f.ctx, "query", 100)
	if len(before) != len(after) {
		t.Fatal("replay produced duplicate outbox events")
	}
	if err = f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvConfirm})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a = f.start(t, "j")
	job = f.complete(t, a, sched.StageOutcome{Kind: sched.Completed})
	if job.Position.State != Resolving || f.dispatched[len(f.dispatched)-1] != kernel.StageResolve {
		t.Fatal("next stage not registered")
	}
	// The completed marker, references and state share the transaction.
	if err = f.commit(func(tx store.Tx) error {
		txn, _ := store.InTransaction(tx)
		var status string
		if err := txn.QueryRow("SELECT status FROM jobs_stage WHERE attempt=?", a.AttemptID).Scan(&status); err != nil {
			return err
		}
		if status != string(sched.Completed) {
			t.Fatalf("marker=%s", status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryRollbackLeavesAttemptAndDispatchUnchanged(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	a := f.start(t, "j")
	before := f.get(t, "j")
	dispatches := len(f.dispatched)
	abort := errors.New("fixture rollback")
	err := f.commit(func(tx store.Tx) error {
		if _, err := f.repo.CompleteStage(tx, a, sched.StageOutcome{Kind: sched.Completed}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	after := f.get(t, "j")
	if after.Version != before.Version || after.AttemptID != before.AttemptID || after.Position.State != before.Position.State || len(f.dispatched) != dispatches {
		t.Fatal("rolled back outcome became visible")
	}
	f.complete(t, a, sched.StageOutcome{Kind: sched.Completed})
}

func TestRepositoryInvalidatesAttemptOnControlChange(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	a := f.start(t, "j")
	job := f.get(t, "j")
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvPause})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.ws.Admit(a); !errors.Is(err, workspace.ErrStale) {
		t.Fatalf("old attempt admitted after pause: %v", err)
	}
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.CompleteStage(tx, a, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("paused result committed: %v", err)
	}
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvResume})
		return err
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale command accepted: %v", err)
	}
}

func TestRepositoryRecoveryPausesWithoutDiscardingCompletedArtifacts(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	a := f.start(t, "j")
	f.complete(t, a, sched.StageOutcome{Kind: sched.Completed, Artifacts: []kernel.ArtifactRef{{Kind: "preflight", ID: "p1", Version: 1}}})
	job := f.get(t, "j")
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvConfirm})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	interrupted := f.start(t, "j")
	before := len(f.dispatched)
	if err := f.commit(func(tx store.Tx) error { _, err := f.repo.RecoverMember(tx, "j"); return err }); err != nil {
		t.Fatal(err)
	}
	job = f.get(t, "j")
	if job.Position.State != Paused || job.Position.Resume != ExtractingDetecting || job.AttemptID != "" || len(f.dispatched) != before {
		t.Fatalf("startup auto-dispatched or lost resume: %+v", job)
	}
	if err := f.ws.Admit(interrupted); !errors.Is(err, workspace.ErrStale) {
		t.Fatalf("old result admitted: %v", err)
	}
	if err := f.commit(func(tx store.Tx) error {
		txn, _ := store.InTransaction(tx)
		var status string
		var artifacts []byte
		if err := txn.QueryRow("SELECT status,artifacts FROM jobs_stage WHERE attempt=?", a.AttemptID).Scan(&status, &artifacts); err != nil {
			return err
		}
		if status != "completed" || len(artifacts) == 0 {
			t.Fatal("completed stage lost during recovery")
		}
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvResume})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumed := f.start(t, "j")
	if resumed.AttemptID == interrupted.AttemptID || f.get(t, "j").Position.State != ExtractingDetecting {
		t.Fatal("resumed wrong stage or reused old attempt")
	}
}

func TestRepositoryReopenKeepsAttemptsCountersAndArtifacts(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	a := f.start(t, "j")
	original := f.get(t, "j")
	if err := f.ws.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(f.dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := store.NewRegistry(Module())
	db, err := store.Open(f.ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(nil, DefaultLimits())
	ws, err := workspace.Open(f.dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if ws.Generation() <= a.OwnerGeneration {
		t.Fatal("reopen reused execution generation")
	}
	if err = ws.Admit(a); !errors.Is(err, workspace.ErrStale) {
		t.Fatalf("previous owner result accepted: %v", err)
	}
	if err = ws.Commit(f.ctx, func(tx store.Tx) error {
		persisted, err := repo.Get(tx, "j")
		if err != nil {
			return err
		}
		if persisted.Version != original.Version || persisted.AttemptID != a.AttemptID || persisted.Position != original.Position {
			t.Fatal("task changed merely by reopening")
		}
		_, err = repo.RecoverMember(tx, "j")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = ws.Commit(f.ctx, func(tx store.Tx) error {
		persisted, err := repo.Get(tx, "j")
		if err != nil {
			return err
		}
		if persisted.Position.State != Paused || persisted.AttemptID != "" {
			t.Fatal("recovered task not paused")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryBoundedPagesAndActiveSlotRelease(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	if err := f.commit(func(tx store.Tx) error {
		for i, id := range []string{"j1", "j2", "j3"} {
			if err := f.repo.AddMember(tx, "b", id, i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error {
		first, err := f.repo.Members(tx, "b", -1, 2)
		if err != nil {
			return err
		}
		second, err := f.repo.Members(tx, "b", first[1].Ordinal, 2)
		if err != nil {
			return err
		}
		if len(first) != 2 || len(second) != 1 || second[0].ID != "j3" {
			t.Fatal("bounded cursor query lost or repeated members")
		}
		return f.repo.FinishBatch(tx, "b")
	}); !errors.Is(err, ErrBatchOpen) {
		t.Fatalf("unfinished batch released: %v", err)
	}
	for _, id := range []string{"j1", "j2", "j3"} {
		a := f.start(t, id)
		f.complete(t, a, sched.StageOutcome{Kind: sched.Failed})
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.FinishBatch(tx, "b") }); err != nil {
		t.Fatal(err)
	}
	f.create(t, "next", RegularBatch)
}

// A file cancelled before the batch starts is left out of the start, as one that cannot be processed is: the other
// files must not lose their start to it (#41).
func TestConfirmingABatchLeavesOutAFileCancelledBeforeIt(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	if err := f.commit(func(tx store.Tx) error {
		if err := f.repo.AddMember(tx, "b", "j1", 0); err != nil {
			return err
		}
		return f.repo.AddMember(tx, "b", "j2", 1)
	}); err != nil {
		t.Fatal(err)
	}
	f.complete(t, f.start(t, "j2"), sched.StageOutcome{Kind: sched.Completed})
	j1 := f.get(t, "j1")
	if err := f.commit(func(tx store.Tx) error {
		next, err := f.repo.Apply(tx, "j1", j1.Version, Event{Kind: EvCancel})
		if err != nil {
			return err
		}
		_, err = f.repo.Apply(tx, "j1", next.Version, Event{Kind: EvCancelDone})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err }); err != nil {
		t.Fatalf("the start refused for a cancelled file: %v", err)
	}
	if s := f.get(t, "j1").Position.State; s != Cancelled {
		t.Fatalf("the cancelled file is %s", s)
	}
	if s := f.get(t, "j2").Position.State; s != Queued {
		t.Fatalf("the other file is %s", s)
	}
}

func TestRepositoryBatchConfirmationIsAtomicAcrossMembers(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	if err := f.commit(func(tx store.Tx) error {
		if err := f.repo.AddMember(tx, "b", "j1", 0); err != nil {
			return err
		}
		return f.repo.AddMember(tx, "b", "j2", 1)
	}); err != nil {
		t.Fatal(err)
	}
	first := f.start(t, "j1")
	f.complete(t, first, sched.StageOutcome{Kind: sched.Completed})
	before := len(f.dispatched)
	if err := f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err }); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unfinished preflight was confirmed: %v", err)
	}
	if f.get(t, "j1").Position.State != Preflighted || len(f.dispatched) != before {
		t.Fatal("failed confirmation changed an earlier member or dispatched it")
	}
	second := f.start(t, "j2")
	f.complete(t, second, sched.StageOutcome{Kind: sched.Completed, Detail: DetailPasswordRequired})
	if err := f.commit(func(tx store.Tx) error {
		count, err := f.repo.ConfirmBatch(tx, "b")
		if count != 2 {
			t.Fatalf("confirmation scope=%d", count)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.get(t, "j1").Position.State != Queued || f.get(t, "j2").Position.Block != BlockPassword {
		t.Fatal("confirmation did not queue ready member and block locked member")
	}
	if len(f.dispatched) != before+1 || f.dispatched[len(f.dispatched)-1] != kernel.StageExtractDetect {
		t.Fatal("ready member not dispatched once after complete confirmation")
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, "b", "extra", 2) }); !errors.Is(err, ErrJobRecord) {
		t.Fatalf("frozen membership changed: %v", err)
	}
}

func TestRepositoryFrozenMembershipCannotGrow(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	a := f.start(t, "j")
	job := f.complete(t, a, sched.StageOutcome{Kind: sched.Completed})
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvConfirm})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, "b", "new", 1) }); !errors.Is(err, ErrJobRecord) {
		t.Fatalf("new file entered frozen batch: %v", err)
	}
}

// A notification can outlive the position it was sent for (#40). Begun anyway, the attempt would be recorded as the
// stage the file is at while the scheduler ran the one the notification named.
func TestBeginStageRefusesAStageTheFileIsNotAt(t *testing.T) {
	R, E, U := kernel.StageResolve, kernel.StageExport, kernel.StagePublish
	cases := []struct {
		name     string
		position Snapshot
		stage    kernel.StageKind
		begins   State // the state it begins at; empty: refused
	}{
		{"between resolve and export", Snapshot{State: Exporting}, R, ""},
		{"ready to publish", Snapshot{State: ReadyToPublish}, R, ""},
		{"queued for another stage", Snapshot{State: Queued, Cursor: R}, U, ""},
		{"waiting for review", Snapshot{State: AwaitingReview}, R, ""},
		{"paused", Snapshot{State: Paused, Resume: Exporting}, E, ""},
		{"cancelling", Snapshot{State: Cancelling}, R, ""},
		{"ended", Snapshot{State: Completed}, U, ""},
		{"no stage named, none to run", Snapshot{State: AwaitingReview}, kernel.StageNone, ""},
		{"queued with a cursor that names no stage", Snapshot{State: Queued, Cursor: kernel.StageSupplementScan}, kernel.StageSupplementScan, ""},
		// what a current notification names still begins
		{"at its stage", Snapshot{State: Exporting}, E, Exporting},
		{"ready, with the publish", Snapshot{State: ReadyToPublish}, U, Publishing},
		{"queued for this stage", Snapshot{State: Queued, Cursor: R}, R, Resolving},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := fixtureDB(t)
			f.create(t, "b", RegularBatch)
			f.member(t, "b", "j")
			before := f.get(t, "j")
			before.Position = c.position
			if err := f.commit(func(tx store.Tx) error {
				_, scope, err := transaction(tx)
				if err != nil {
					return err
				}
				return save(tx, scope, before, before.Version)
			}); err != nil {
				t.Fatal(err)
			}
			var a kernel.AttemptRef
			err := f.commit(func(tx store.Tx) (err error) {
				a, err = f.repo.BeginStage(tx, "j", c.stage, before.Version, f.ws.Generation())
				return err
			})
			after := f.get(t, "j")
			if c.begins != "" {
				if err != nil || a.AttemptID == "" || after.Position.State != c.begins {
					t.Fatalf("a current notification: %v, attempt %q, state %s", err, a.AttemptID, after.Position.State)
				}
				return
			}
			if !errors.Is(err, ErrStaleDispatch) {
				t.Fatalf("began %s at %s: %v, state now %s", c.stage, c.position.State, err, after.Position.State)
			}
			if after.Version != before.Version || after.AttemptID != "" || after.Position.State != c.position.State {
				t.Fatalf("a refused notification changed the file: %+v", after)
			}
		})
	}
}

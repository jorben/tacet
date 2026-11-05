//go:build darwin || windows

package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

// These controlled runners exercise orchestration and persistence, not document
// capability. Real plugin stages have their own protocol/format acceptance.
type pipelineRunner struct {
	stage   kernel.StageKind
	outcome sched.OutcomeKind
}

func (r pipelineRunner) Kind() kernel.StageKind { return r.stage }
func (r pipelineRunner) Claim(sched.JobView) kernel.ResourceClaim {
	return kernel.ResourceClaim{MemoryBytes: 32, CPUSlots: 1, WorkerKind: "format"}
}
func (r pipelineRunner) Run(ctx sched.StageContext) sched.StageOutcome {
	if ctx.Attempt().AttemptID == "" {
		return sched.StageOutcome{Kind: sched.Failed}
	}
	return sched.StageOutcome{Kind: r.outcome}
}

func TestSchedulerStageOutcomesPersistThroughWorkspaceAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := store.NewRegistry(Module())
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gate := netgate.NewGate()
	q, err := sched.NewQueue(sched.Budget{MemoryBytes: 256, CPUSlots: 1, ScratchBytes: 256, CandidateBytes: 256, QueueBytes: 256, MaxRunning: 1, MaxQueued: 10, Workers: map[string]int{"format": 1}}, gate,
		pipelineRunner{kernel.StagePreflight, sched.Completed}, pipelineRunner{kernel.StageExtractDetect, sched.Completed}, pipelineRunner{kernel.StageResolve, sched.NeedsReview})
	if err != nil {
		t.Fatal(err)
	}
	var ws *workspace.Workspace
	var repo *Repository
	dispatchErrors := make(chan error, 10)
	repo = NewRepository(func(id string, stage kernel.StageKind) {
		var a kernel.AttemptRef
		err := ws.Commit(ctx, func(tx store.Tx) error {
			j, err := repo.Get(tx, id)
			if err != nil {
				return err
			}
			a, err = repo.BeginStage(tx, id, stage, j.Version, ws.Generation())
			return err
		})
		if err == nil {
			err = q.Submit(sched.JobView{Attempt: a, Stage: stage, Priority: kernel.Interactive})
		}
		if err != nil {
			dispatchErrors <- err
		}
	}, DefaultLimits())
	ws, err = workspace.Open(dir, workspace.BackendFunc(func(ctx context.Context) (workspace.Txn, error) { return db.Begin(ctx) }), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err = ws.Commit(ctx, func(tx store.Tx) error {
		if err := repo.CreateDraft(tx, "quick", QuickBatch); err != nil {
			return err
		}
		return repo.AddMember(tx, "quick", "j", 0)
	}); err != nil {
		t.Fatal(err)
	}
	completed := make(chan State, 3)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- sched.NewDispatcher(q).Run(runCtx, func(view sched.JobView, out sched.StageOutcome) error {
			var position State
			err := ws.Commit(ctx, func(tx store.Tx) error {
				if err := ws.AdmitTx(tx, view.Attempt); err != nil {
					return err
				}
				j, err := repo.CompleteStage(tx, view.Attempt, out)
				if err != nil {
					return err
				}
				position = j.Position.State
				return nil
			})
			if err == nil {
				completed <- position
				if position == AwaitingReview {
					stop()
				}
			}
			return err
		})
	}()
	select {
	case state := <-completed:
		if state != Preflighted {
			t.Fatalf("preflight=%s", state)
		}
	case <-ctx.Done():
		t.Fatal("preflight did not persist")
	}
	if err = ws.Commit(ctx, func(tx store.Tx) error { _, err := repo.ConfirmBatch(tx, "quick"); return err }); err != nil {
		t.Fatal(err)
	}
	for _, want := range []State{Resolving, AwaitingReview} {
		select {
		case got := <-completed:
			if got != want {
				t.Fatalf("state=%s want=%s", got, want)
			}
		case <-ctx.Done():
			t.Fatal("stage pipeline did not advance")
		}
	}
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("dispatcher did not stop")
	}
	if len(dispatchErrors) != 0 {
		t.Fatal(<-dispatchErrors)
	}
	if q.State().Running != 0 || len(gate.State()) != 0 {
		t.Fatal("review wait kept a worker reservation or processing lease")
	}
	if err = ws.Commit(ctx, func(tx store.Tx) error {
		j, err := repo.Get(tx, "j")
		if err != nil {
			return err
		}
		if j.Position.State != AwaitingReview || j.AttemptID != "" {
			t.Fatal("final review state not durable")
		}
		concrete, _ := store.InTransaction(tx)
		var count int
		if err = concrete.QueryRow("SELECT COUNT(*) FROM jobs_stage WHERE status IN ('completed','needs_review')").Scan(&count); err != nil {
			return err
		}
		if count != 3 {
			t.Fatalf("stage records=%d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

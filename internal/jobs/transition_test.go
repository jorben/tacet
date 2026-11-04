package jobs

import (
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/validate"
)

func outcome(stage kernel.StageKind, kind sched.OutcomeKind, detail string) Event {
	return Event{Kind: EvStageOutcome, Stage: stage, Outcome: sched.StageOutcome{Kind: kind, Detail: detail}}
}

func step(t *testing.T, s Snapshot, e Event) Result {
	t.Helper()
	r, err := Transition(s, e, DefaultLimits())
	if err != nil {
		t.Fatalf("%s in %s: %v", e.Kind, s.State, err)
	}
	return r
}

// Rows of the module design 5.3 transition table.
func TestTransitionTableRows(t *testing.T) {
	P, X, R, E, V, U := kernel.StagePreflight, kernel.StageExtractDetect, kernel.StageResolve, kernel.StageExport, kernel.StageValidate, kernel.StagePublish
	cases := []struct {
		name     string
		from     Snapshot
		event    Event
		state    State
		dispatch kernel.StageKind
		exc      exceptions.Type
	}{
		{"probe ok", Snapshot{State: Preflighting}, outcome(P, sched.Completed, ""), Preflighted, "", ""},
		{"probe needs password", Snapshot{State: Preflighting}, outcome(P, sched.Completed, DetailPasswordRequired), Locked, "", ""},
		{"probe rejects file", Snapshot{State: Preflighting}, outcome(P, sched.Failed, "page_limit"), Unprocessable, "", ""},
		{"confirm", Snapshot{State: Preflighted}, Event{Kind: EvConfirm}, Queued, "", ""},
		{"confirm locked", Snapshot{State: Locked}, Event{Kind: EvConfirm}, Blocked, "", exceptions.PasswordRequired},
		{"unlock from blocked", Snapshot{State: Blocked, Block: BlockPassword}, Event{Kind: EvUnlocked}, Preflighting, P, ""},
		{"dispatch queued", Snapshot{State: Queued}, Event{Kind: EvDispatch}, ExtractingDetecting, X, ""},
		{"extract done", Snapshot{State: ExtractingDetecting}, outcome(X, sched.Completed, ""), Resolving, R, ""},
		{"resolve clean", Snapshot{State: Resolving}, outcome(R, sched.Completed, ""), Exporting, E, ""},
		{"resolve pending", Snapshot{State: Resolving}, outcome(R, sched.NeedsReview, ""), AwaitingReview, "", ""},
		{"review decided", Snapshot{State: AwaitingReview}, Event{Kind: EvDecisionsChange}, Resolving, R, ""},
		{"export done", Snapshot{State: Exporting}, outcome(E, sched.Completed, ""), Validating, V, ""},
		{"validation passed", Snapshot{State: Validating}, outcome(V, sched.Completed, string(validate.Passed)), ReadyToPublish, "", ""},
		{"new findings", Snapshot{State: Validating}, outcome(V, sched.Completed, string(validate.NewFindings)), Resolving, R, ""},
		{"authorized publish", Snapshot{State: ReadyToPublish}, Event{Kind: EvDispatch}, Publishing, U, ""},
		{"published", Snapshot{State: Publishing}, outcome(U, sched.Completed, ""), Completed, "", ""},
		{"published partial", Snapshot{State: Publishing}, outcome(U, sched.Completed, DetailPartial), Partial, "", ""},
		{"name conflict", Snapshot{State: Publishing}, outcome(U, sched.Blocked, string(BlockTarget)), Blocked, "", ""},
		{"stage fails", Snapshot{State: Exporting}, outcome(E, sched.Failed, ""), Failed, "", exceptions.FileFailed},
		{"skip failed", Snapshot{State: Failed, FailedStage: E}, Event{Kind: EvSkip}, Skipped, "", ""},
		{"pause", Snapshot{State: Exporting}, Event{Kind: EvPause}, Paused, "", ""},
		{"cancel", Snapshot{State: AwaitingReview}, Event{Kind: EvCancel}, Cancelling, "", ""},
		{"cancel done", Snapshot{State: Cancelling}, Event{Kind: EvCancelDone}, Cancelled, "", ""},
		{"cancel acknowledged by stage", Snapshot{State: Cancelling}, outcome(E, sched.Cancelled, ""), Cancelled, "", ""},
		{"upstream became stale", Snapshot{State: Validating}, Event{Kind: EvInvalidate, Stage: R}, Queued, "", ""},
		{"input changed", Snapshot{State: Exporting}, Event{Kind: EvInputChanged}, Blocked, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := step(t, c.from, c.event)
			if r.Snapshot.State != c.state || r.Dispatch != c.dispatch || r.Exception != c.exc || r.Discard {
				t.Fatalf("got state=%s dispatch=%q exc=%q discard=%v", r.Snapshot.State, r.Dispatch, r.Exception, r.Discard)
			}
		})
	}
}

func TestValidationRetryBudgetsAndFailures(t *testing.T) {
	V, E := kernel.StageValidate, kernel.StageExport
	s := Snapshot{State: Validating}
	// not_run: two automatic reruns reusing the candidate, the third fails the file.
	for i := 1; i <= 2; i++ {
		r := step(t, s, outcome(V, sched.Completed, string(validate.NotRun)))
		if r.Snapshot.State != Validating || r.Dispatch != V || r.Snapshot.Counters.ValidateReruns != i {
			t.Fatalf("rerun %d: %+v", i, r)
		}
		s = r.Snapshot
	}
	r := step(t, s, outcome(V, sched.Completed, string(validate.NotRun)))
	if r.Snapshot.State != Failed || r.Exception != exceptions.FileFailed || r.Snapshot.FailedStage != V {
		t.Fatalf("exhausted reruns: %+v", r)
	}
	// widening happens once per file, a second residual fails it.
	s = Snapshot{State: Validating}
	r = step(t, s, outcome(V, sched.Completed, string(validate.Widened)))
	if r.Snapshot.State != Exporting || r.Dispatch != E || r.Snapshot.Counters.Widened != 1 {
		t.Fatalf("widen: %+v", r)
	}
	again := r.Snapshot
	again.State = Validating
	if r = step(t, again, outcome(V, sched.Completed, string(validate.Widened))); r.Snapshot.State != Failed {
		t.Fatalf("second widen must fail the file: %+v", r)
	}
	// corrupt, un-widenable residual and unknown conclusions all fail.
	for _, c := range []string{string(validate.FailedCorrupt), string(validate.FailedResidual), "", "something_new"} {
		if r = step(t, Snapshot{State: Validating}, outcome(V, sched.Completed, c)); r.Snapshot.State != Failed {
			t.Fatalf("conclusion %q must fail, got %s", c, r.Snapshot.State)
		}
	}
}

func TestReprocessIsOfferedOnceThenUnprocessable(t *testing.T) {
	E := kernel.StageExport
	r := step(t, Snapshot{State: Exporting}, outcome(E, sched.Failed, ""))
	if r.Snapshot.State != Failed {
		t.Fatal("first failure must be reprocessable")
	}
	r = step(t, r.Snapshot, Event{Kind: EvReprocess})
	if r.Snapshot.State != Queued || r.Snapshot.Cursor != E || r.Snapshot.Counters.Reprocessed != 1 {
		t.Fatalf("reprocess restarts at the failed stage: %+v", r.Snapshot)
	}
	r = step(t, r.Snapshot, Event{Kind: EvDispatch})
	if r.Snapshot.State != Exporting || r.Dispatch != E {
		t.Fatalf("redispatch: %+v", r)
	}
	r = step(t, r.Snapshot, outcome(E, sched.Failed, ""))
	if r.Snapshot.State != Unprocessable || r.Exception != "" {
		t.Fatalf("second failure is final: %+v", r)
	}
	if _, err := Transition(Snapshot{State: Failed, FailedStage: E, Counters: Counters{Reprocessed: 1}}, Event{Kind: EvReprocess}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("second reprocess offered")
	}
}

func TestResourceBlockDoesNotCountAndRequeuesAtTheStage(t *testing.T) {
	E := kernel.StageExport
	for _, kind := range []sched.OutcomeKind{sched.Blocked, sched.Retry} {
		r := step(t, Snapshot{State: Exporting}, outcome(E, kind, "disk_full"))
		if r.Snapshot.State != Blocked || r.Snapshot.Block != BlockResource || r.Snapshot.Counters != (Counters{}) {
			t.Fatalf("%s: %+v", kind, r.Snapshot)
		}
		r = step(t, r.Snapshot, Event{Kind: EvUnblock})
		if r.Snapshot.State != Queued || r.Snapshot.Cursor != E {
			t.Fatalf("unblock: %+v", r.Snapshot)
		}
	}
	// A password block is not cleared by a resource event.
	if _, err := Transition(Snapshot{State: Blocked, Block: BlockPassword, Cursor: E}, Event{Kind: EvUnblock}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("password block released by unblock")
	}
}

// A decision made while a file is stopped reaches what is published: without moving back, the retry would go on
// from a candidate made from the old decisions (publish does not read them again).
func TestADecisionUnderAStoppedFileIsWhatItsRetryPublishes(t *testing.T) {
	R, U, X := kernel.StageResolve, kernel.StagePublish, kernel.StageExtractDetect
	blocked := step(t, Snapshot{State: Blocked, Block: BlockTarget, Cursor: U}, Event{Kind: EvDecisionsChange})
	if s := blocked.Snapshot; s.State != Blocked || s.Block != BlockTarget || s.Cursor != R || blocked.Dispatch != kernel.StageNone {
		t.Fatalf("a decision released or forgot the block: %+v dispatch %q", s, blocked.Dispatch)
	}
	if s := step(t, blocked.Snapshot, Event{Kind: EvUnblock}).Snapshot; s.State != Queued || s.Cursor != R {
		t.Fatalf("the retry after a decision: %+v", s)
	}
	failed := step(t, Snapshot{State: Failed, FailedStage: U}, Event{Kind: EvDecisionsChange})
	if s := failed.Snapshot; s.State != Failed || s.FailedStage != R || failed.Dispatch != kernel.StageNone {
		t.Fatalf("a decision released or left the failed file: %+v", s)
	}
	if s := step(t, failed.Snapshot, Event{Kind: EvReprocess}).Snapshot; s.State != Queued || s.Cursor != R {
		t.Fatalf("the reprocess after a decision: %+v", s)
	}
	// without a decision a retry goes on where it stopped
	if s := step(t, Snapshot{State: Blocked, Block: BlockTarget, Cursor: U}, Event{Kind: EvUnblock}).Snapshot; s.Cursor != U {
		t.Fatalf("the retry without a decision: %+v", s)
	}
	// a file that failed before resolution is not moved past where it stopped
	if s := step(t, Snapshot{State: Failed, FailedStage: X}, Event{Kind: EvDecisionsChange}).Snapshot; s.FailedStage != X {
		t.Fatalf("moved forward: %+v", s)
	}
}

func TestPauseDiscardsAbandonedAttemptAndRedoesStageOnResume(t *testing.T) {
	X := kernel.StageExtractDetect
	paused := step(t, Snapshot{State: ExtractingDetecting}, Event{Kind: EvPause}).Snapshot
	late := step(t, paused, outcome(X, sched.Completed, ""))
	if !late.Discard || late.Snapshot.State != Paused || late.Dispatch != "" {
		t.Fatalf("late result must be discarded: %+v", late)
	}
	late = step(t, paused, outcome(X, sched.Cancelled, ""))
	if !late.Discard || late.Snapshot.State != Paused {
		t.Fatalf("cancel ack while paused: %+v", late)
	}
	r := step(t, paused, Event{Kind: EvResume})
	if r.Snapshot.State != Queued || r.Snapshot.Cursor != X {
		t.Fatalf("resume redoes the stage: %+v", r.Snapshot)
	}
	// Waiting states return to themselves, not to a stage.
	waiting := step(t, Snapshot{State: AwaitingReview}, Event{Kind: EvPause}).Snapshot
	if r = step(t, waiting, Event{Kind: EvResume}); r.Snapshot.State != AwaitingReview || r.Dispatch != "" {
		t.Fatalf("resume of a waiting task: %+v", r)
	}
	// While cancelling, results other than the acknowledgement are discarded.
	cancelling := step(t, Snapshot{State: Exporting}, Event{Kind: EvCancel}).Snapshot
	if r = step(t, cancelling, outcome(kernel.StageExport, sched.Completed, "")); !r.Discard || r.Snapshot.State != Cancelling {
		t.Fatalf("result during cancel: %+v", r)
	}
}

func TestUndefinedCombinationsAreRejected(t *testing.T) {
	bad := []struct {
		name string
		s    Snapshot
		e    Event
	}{
		{"terminal state", Snapshot{State: Completed}, Event{Kind: EvPause}},
		{"result for another stage", Snapshot{State: Exporting}, outcome(kernel.StageValidate, sched.Completed, "")},
		{"result while queued", Snapshot{State: Queued}, outcome(kernel.StageExport, sched.Completed, "")},
		{"review requested outside resolve", Snapshot{State: Exporting}, outcome(kernel.StageExport, sched.NeedsReview, "")},
		{"unrequested cancellation", Snapshot{State: Exporting}, outcome(kernel.StageExport, sched.Cancelled, "")},
		{"unknown outcome kind", Snapshot{State: Exporting}, outcome(kernel.StageExport, "teapot", "")},
		{"confirm a running task", Snapshot{State: Exporting}, Event{Kind: EvConfirm}},
		{"dispatch a running task", Snapshot{State: Exporting}, Event{Kind: EvDispatch}},
		{"resume a task that is not paused", Snapshot{State: Exporting}, Event{Kind: EvResume}},
		{"reprocess a non-failed task", Snapshot{State: Exporting}, Event{Kind: EvReprocess}},
		{"invalidate to preflight", Snapshot{State: Exporting}, Event{Kind: EvInvalidate, Stage: kernel.StagePreflight}},
		{"invalidate to unknown stage", Snapshot{State: Exporting}, Event{Kind: EvInvalidate, Stage: "nope"}},
		{"invalidate a draft", Snapshot{State: Preflighted}, Event{Kind: EvInvalidate, Stage: kernel.StageResolve}},
		{"cancel done without cancelling", Snapshot{State: Exporting}, Event{Kind: EvCancelDone}},
		{"unknown event", Snapshot{State: Exporting}, Event{Kind: "dance"}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Transition(c.s, c.e, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

// Walks the whole happy path, including a review round trip and an invalidation
// of an already exported file, asserting the dispatch sequence the scheduler sees.
func TestEndToEndPathDispatchSequence(t *testing.T) {
	events := []struct {
		e    Event
		want kernel.StageKind
	}{
		{outcome(kernel.StagePreflight, sched.Completed, ""), ""},
		{Event{Kind: EvConfirm}, ""},
		{Event{Kind: EvDispatch}, kernel.StageExtractDetect},
		{outcome(kernel.StageExtractDetect, sched.Completed, ""), kernel.StageResolve},
		{outcome(kernel.StageResolve, sched.NeedsReview, ""), ""},
		{Event{Kind: EvDecisionsChange}, kernel.StageResolve},
		{outcome(kernel.StageResolve, sched.Completed, ""), kernel.StageExport},
		{outcome(kernel.StageExport, sched.Completed, ""), kernel.StageValidate},
		{outcome(kernel.StageValidate, sched.Completed, string(validate.NewFindings)), kernel.StageResolve},
		{outcome(kernel.StageResolve, sched.Completed, ""), kernel.StageExport},
		{outcome(kernel.StageExport, sched.Completed, ""), kernel.StageValidate},
		{outcome(kernel.StageValidate, sched.Completed, string(validate.Passed)), ""},
		{Event{Kind: EvDispatch}, kernel.StagePublish},
		{outcome(kernel.StagePublish, sched.Completed, ""), ""},
	}
	s := Snapshot{State: Preflighting}
	for i, step1 := range events {
		r := step(t, s, step1.e)
		if r.Dispatch != step1.want {
			t.Fatalf("step %d (%s): dispatch %q want %q", i, step1.e.Kind, r.Dispatch, step1.want)
		}
		s = r.Snapshot
	}
	if s.State != Completed || !s.State.Terminal() {
		t.Fatalf("final state %s", s.State)
	}
}

func TestTransitionIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	in := Snapshot{State: Validating, Counters: Counters{ValidateReruns: 1}}
	ev := outcome(kernel.StageValidate, sched.Completed, string(validate.NotRun))
	a := step(t, in, ev)
	b := step(t, in, ev)
	if a != b {
		t.Fatal("same input, different result")
	}
	if in.Counters.ValidateReruns != 1 {
		t.Fatal("input snapshot mutated")
	}
}

func TestInvalidationKeepsPauseAndBlockAndMovesTheRestartPointBack(t *testing.T) {
	R := kernel.StageResolve
	// paused: stays paused, resumes into the stale stage
	r := step(t, Snapshot{State: Paused, Resume: Validating}, Event{Kind: EvInvalidate, Stage: R})
	if r.Snapshot.State != Paused || r.Snapshot.Resume != Queued || r.Snapshot.Cursor != R || r.Dispatch != kernel.StageNone {
		t.Fatalf("paused: %+v", r)
	}
	back := step(t, r.Snapshot, Event{Kind: EvResume})
	if back.Snapshot.State != Queued || back.Snapshot.Cursor != R {
		t.Fatalf("resume after invalidation: %+v", back.Snapshot)
	}
	// blocked: the block (password, input changed, resource) is not cleared
	for _, reason := range []BlockReason{BlockPassword, BlockInputChanged, BlockResource, BlockTarget} {
		r = step(t, Snapshot{State: Blocked, Block: reason, Cursor: kernel.StageExport}, Event{Kind: EvInvalidate, Stage: R})
		if r.Snapshot.State != Blocked || r.Snapshot.Block != reason || r.Snapshot.Cursor != R || r.Dispatch != kernel.StageNone {
			t.Fatalf("blocked (%s): %+v", reason, r)
		}
	}
	// failed: reprocessing must not start after a stale stage, and never later than it did
	r = step(t, Snapshot{State: Failed, FailedStage: kernel.StageValidate}, Event{Kind: EvInvalidate, Stage: R})
	if r.Snapshot.State != Failed || r.Snapshot.FailedStage != R {
		t.Fatalf("failed: %+v", r.Snapshot)
	}
	r = step(t, Snapshot{State: Failed, FailedStage: kernel.StageResolve}, Event{Kind: EvInvalidate, Stage: kernel.StageExport})
	if r.Snapshot.FailedStage != kernel.StageResolve {
		t.Fatalf("a later stale stage moved the failed stage forward: %+v", r.Snapshot)
	}
	// running and waiting states are sent back to the stale stage
	for _, s := range []State{ExtractingDetecting, Resolving, AwaitingReview, Exporting, Validating, ReadyToPublish, Queued} {
		r = step(t, Snapshot{State: s}, Event{Kind: EvInvalidate, Stage: R})
		if r.Snapshot.State != Queued || r.Snapshot.Cursor != R {
			t.Fatalf("%s: %+v", s, r.Snapshot)
		}
	}
}

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

// The resume point of a stopped file only ever moves back: going forward would skip a stage that did not run.
func TestADecisionDoesNotMoveAStoppedFilePastWhereItStopped(t *testing.T) {
	R, X, E := kernel.StageResolve, kernel.StageExtractDetect, kernel.StageExport
	for _, ev := range []Event{{Kind: EvDecisionsChange}, {Kind: EvInvalidate, Stage: R}} {
		if s := step(t, Snapshot{State: Blocked, Block: BlockResource, Cursor: X}, ev).Snapshot; s.State != Blocked || s.Block != BlockResource || s.Cursor != X {
			t.Fatalf("%s moved the resume point forward: %+v", ev.Kind, s)
		}
		if s := step(t, Snapshot{State: Blocked, Block: BlockResource, Cursor: E}, ev).Snapshot; s.Cursor != R {
			t.Fatalf("%s left the resume point after what it changed: %+v", ev.Kind, s)
		}
	}
}

// A decision under a pause changes what the file goes back to, as it would have without the pause: it stays
// paused, and the resume does not go on from what was made from the old decisions.
func TestADecisionUnderAPauseIsWhatItsResumeGoesOnWith(t *testing.T) {
	R, U := kernel.StageResolve, kernel.StagePublish
	decided := func(s Snapshot) Snapshot {
		t.Helper()
		r := step(t, s, Event{Kind: EvDecisionsChange})
		if r.Snapshot.State != Paused || r.Dispatch != kernel.StageNone {
			t.Fatalf("a decision ended the pause of %+v: %+v dispatch %q", s, r.Snapshot, r.Dispatch)
		}
		return r.Snapshot
	}
	resumed := func(s Snapshot) Snapshot {
		t.Helper()
		return step(t, s, Event{Kind: EvResume}).Snapshot
	}
	for _, back := range []State{AwaitingReview, ReadyToPublish, Resolving, Exporting, Validating} {
		if s := resumed(decided(Snapshot{State: Paused, Resume: back, Cursor: U})); s.State != Queued || s.Cursor != R {
			t.Fatalf("paused from %s, then decided and resumed: %+v", back, s)
		}
	}
	// a publish stopped by the pause, or a file queued for a stage past resolution: nothing runs that it would have to replace
	if s := resumed(decided(Snapshot{State: Paused, Resume: Publishing, Cursor: U})); s.State != Queued || s.Cursor != R {
		t.Fatalf("paused from publishing, then decided and resumed: %+v", s)
	}
	for _, cursor := range []kernel.StageKind{kernel.StageExport, kernel.StageValidate, U} {
		if s := resumed(decided(Snapshot{State: Paused, Resume: Queued, Cursor: cursor})); s.State != Queued || s.Cursor != R {
			t.Fatalf("paused while queued for %s, then decided and resumed: %+v", cursor, s)
		}
	}
	if s := resumed(decided(Snapshot{State: Paused, Resume: Blocked, Block: BlockTarget, Cursor: U})); s.State != Blocked || s.Block != BlockTarget || s.Cursor != R {
		t.Fatalf("paused while blocked, then decided and resumed: %+v", s)
	}
	if s := resumed(decided(Snapshot{State: Paused, Resume: Failed, FailedStage: U})); s.State != Failed || s.FailedStage != R {
		t.Fatalf("paused while failed, then decided and resumed: %+v", s)
	}
	// nothing was made yet that a decision could make stale: an event that does not apply, and the caller just keeps the decision
	for _, back := range []State{Preflighting, Preflighted, Locked, ExtractingDetecting} {
		if _, err := Transition(Snapshot{State: Paused, Resume: back}, Event{Kind: EvDecisionsChange}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("paused from %s: %v", back, err)
		}
	}
	if _, err := Transition(Snapshot{State: Paused, Resume: Queued, Cursor: R}, Event{Kind: EvDecisionsChange}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("paused while queued for resolution, which reads the decisions anyway: %v", err)
	}
}

// A file on its way back to a stage past resolution would go on from the old decisions; it goes back to resolution.
func TestADecisionOnAFileQueuedPastResolutionTakesItBackToResolution(t *testing.T) {
	R := kernel.StageResolve
	for _, cursor := range []kernel.StageKind{kernel.StageExport, kernel.StageValidate, kernel.StagePublish} {
		r := step(t, Snapshot{State: Queued, Cursor: cursor}, Event{Kind: EvDecisionsChange})
		if r.Snapshot.State != Queued || r.Snapshot.Cursor != R || !r.Snapshot.Reviewed {
			t.Fatalf("queued for %s: %+v", cursor, r.Snapshot)
		}
	}
	for _, cursor := range []kernel.StageKind{kernel.StageExtractDetect, R} {
		if _, err := Transition(Snapshot{State: Queued, Cursor: cursor}, Event{Kind: EvDecisionsChange}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("queued for %s, which reads the decisions when it gets there: %v", cursor, err)
		}
	}
}

// A document that has gone out or is going out does not follow a new decision, and the caller has to be able to tell
// that from an event that merely does not apply: the second is tolerated, the first must reach the person (#42).
func TestADecisionOnAFilePublishingOrPublishedIsClosedNotJustInvalid(t *testing.T) {
	for _, state := range []State{Publishing, Completed, Partial} {
		_, err := Transition(Snapshot{State: state, Cursor: kernel.StagePublish}, Event{Kind: EvDecisionsChange}, DefaultLimits())
		if !errors.Is(err, ErrDecisionsClosed) || errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: %v", state, err)
		}
	}
	// nothing goes out of these, so nothing depends on the decision
	for _, state := range []State{Skipped, Unprocessable, Cancelled, Cancelling, ExtractingDetecting, Preflighting, Preflighted, Locked} {
		if _, err := Transition(Snapshot{State: state}, Event{Kind: EvDecisionsChange}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: %v", state, err)
		}
	}
	// any other event on an ended file stays what it was
	if _, err := Transition(Snapshot{State: Completed}, Event{Kind: EvPause}, DefaultLimits()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("a pause of a completed file: %v", err)
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

// A person changed the file and waits: it stays ahead of background work through to publishing, and until then only
// going back to review ends that (AR-16).
func TestADecisionKeepsTheFileAheadUntilItIsBackUnderReview(t *testing.T) {
	R, E, V, U := kernel.StageResolve, kernel.StageExport, kernel.StageValidate, kernel.StagePublish
	s := Snapshot{State: AwaitingReview}
	if s.Reviewed {
		t.Fatal("reviewed before anybody decided")
	}
	s = step(t, s, Event{Kind: EvDecisionsChange}).Snapshot
	for _, e := range []Event{
		{Kind: EvStageOutcome, Stage: R, Outcome: sched.StageOutcome{Kind: sched.Completed}},
		{Kind: EvStageOutcome, Stage: E, Outcome: sched.StageOutcome{Kind: sched.Completed}},
		{Kind: EvStageOutcome, Stage: V, Outcome: sched.StageOutcome{Kind: sched.Completed, Detail: string(validate.Passed)}},
		{Kind: EvDispatch},
	} {
		if !s.Reviewed {
			t.Fatalf("lost before %s, in %s", e.Kind, s.State)
		}
		s = step(t, s, e).Snapshot
	}
	if s.State != Publishing || !s.Reviewed || U != stageOf[s.State] {
		t.Fatalf("at publishing: %+v", s)
	}
	back := step(t, Snapshot{State: Resolving, Reviewed: true}, Event{Kind: EvStageOutcome, Stage: R, Outcome: sched.StageOutcome{Kind: sched.NeedsReview}}).Snapshot
	if back.State != AwaitingReview || back.Reviewed {
		t.Fatalf("back under review: %+v", back)
	}
	// wherever the file stands when the decision comes
	for _, state := range []State{Exporting, Validating, ReadyToPublish, Resolving} {
		if got := step(t, Snapshot{State: state}, Event{Kind: EvDecisionsChange}).Snapshot; !got.Reviewed {
			t.Errorf("a decision in %s: %+v", state, got)
		}
	}
	if got := step(t, Snapshot{State: Failed, FailedStage: V}, Event{Kind: EvDecisionsChange}).Snapshot; !got.Reviewed {
		t.Errorf("a decision on a failed file: %+v", got)
	}
	if got := step(t, Snapshot{State: Paused, Resume: AwaitingReview}, Event{Kind: EvDecisionsChange}).Snapshot; !got.Reviewed || got.State != Paused {
		t.Errorf("a decision under a pause: %+v", got)
	}
}

// What it takes for a file, and so for a started batch of such files, to wait for a person (FR-28).
func TestWhichStatesWaitForAPerson(t *testing.T) {
	waiting := []State{AwaitingReview, Blocked, Failed, Paused, Locked, Completed, Partial, Skipped, Unprocessable, Cancelled}
	working := []State{Preflighting, Queued, ExtractingDetecting, Resolving, Exporting, Validating, Publishing}
	for _, autoPublish := range []bool{false, true} {
		for _, s := range waiting {
			if !s.WaitsForPerson(autoPublish) {
				t.Errorf("%s with auto publish %v does not wait", s, autoPublish)
			}
		}
		for _, s := range working {
			if s.WaitsForPerson(autoPublish) {
				t.Errorf("%s with auto publish %v waits", s, autoPublish)
			}
		}
	}
	if !ReadyToPublish.WaitsForPerson(false) || ReadyToPublish.WaitsForPerson(true) {
		t.Error("a file ready to publish waits only until publishing is authorized")
	}
}

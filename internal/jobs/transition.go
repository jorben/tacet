package jobs

import (
	"errors"

	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/validate"
)

// ErrInvalidTransition is returned for any (state, event) pair the module
// design does not define. Unknown combinations are never accepted silently
// (MD-07, INV-02).
var ErrInvalidTransition = errors.New("JOB_TRANSITION_INVALID")

// BlockReason says why a task is Blocked.
type BlockReason string

const (
	BlockPassword     BlockReason = "password_required"
	BlockInputChanged BlockReason = "input_changed"
	BlockTarget       BlockReason = "target"
	BlockResource     BlockReason = "resource"
)

// Preflight outcome details (the closed set handed over by preflight).
const (
	DetailPasswordRequired = "password_required"
	DetailPartial          = "partial" // publish result: a partial result was delivered
)

// Limits are the automatic retry budgets (module design 5.3). Validation
// reruns default to 2 until M0 freezes the value.
type Limits struct {
	MaxValidateReruns int
	MaxWiden          int
	MaxReprocess      int
}

func DefaultLimits() Limits { return Limits{MaxValidateReruns: 2, MaxWiden: 1, MaxReprocess: 1} }

// Counters are per-file and survive restarts with the task record.
type Counters struct {
	ValidateReruns int
	Widened        int
	Reprocessed    int
}

// Snapshot is the state-machine input: the persisted position of one file task.
type Snapshot struct {
	State       State
	Cursor      kernel.StageKind // stage to run when a Queued task is dispatched
	FailedStage kernel.StageKind // stage that failed, for the one reprocess
	Resume      State            // state to return to after Paused
	Block       BlockReason
	Counters    Counters
}

// EventKind is the closed set of inputs to the state machine.
type EventKind string

const (
	EvStageOutcome    EventKind = "stage_outcome"
	EvConfirm         EventKind = "confirm"          // batch confirmed; snapshot frozen by the caller
	EvDispatch        EventKind = "dispatch"         // scheduler picked the task / publish authorized
	EvDecisionsChange EventKind = "decisions_change" // review decisions committed
	EvUnlocked        EventKind = "unlocked"         // password accepted
	EvPause           EventKind = "pause"
	EvResume          EventKind = "resume"
	EvCancel          EventKind = "cancel"
	EvCancelDone      EventKind = "cancel_done" // running work stopped or none was running
	EvReprocess       EventKind = "reprocess"
	EvSkip            EventKind = "skip"
	EvInvalidate      EventKind = "invalidate"    // lineage found stale upstream; Stage is the earliest stale stage
	EvInputChanged    EventKind = "input_changed" // fingerprint check failed (FR-08)
	EvUnblock         EventKind = "unblock"       // resource/target condition cleared
)

// Event carries the stage that produced an outcome, or the earliest stale stage.
type Event struct {
	Kind    EventKind
	Stage   kernel.StageKind
	Outcome sched.StageOutcome
}

// Result is the state-machine output. Dispatch is the stage to submit to the
// scheduler after the transaction commits (MD-10); Discard means the event was a
// late result for an abandoned attempt and nothing changed.
type Result struct {
	Snapshot  Snapshot
	Dispatch  kernel.StageKind
	Exception exceptions.Type
	Discard   bool
}

var stageOf = map[State]kernel.StageKind{
	Preflighting:        kernel.StagePreflight,
	ExtractingDetecting: kernel.StageExtractDetect,
	Resolving:           kernel.StageResolve,
	Exporting:           kernel.StageExport,
	Validating:          kernel.StageValidate,
	Publishing:          kernel.StagePublish,
}

var stateOf = map[kernel.StageKind]State{
	kernel.StagePreflight:     Preflighting,
	kernel.StageExtractDetect: ExtractingDetecting,
	kernel.StageResolve:       Resolving,
	kernel.StageExport:        Exporting,
	kernel.StageValidate:      Validating,
	kernel.StagePublish:       Publishing,
}

// stageRank is the pipeline order, for comparing two stages.
var stageRank = map[kernel.StageKind]int{
	kernel.StagePreflight: 1, kernel.StageExtractDetect: 2, kernel.StageResolve: 3,
	kernel.StageExport: 4, kernel.StageValidate: 5, kernel.StagePublish: 6,
}

func (s State) running() bool { _, ok := stageOf[s]; return ok }

// pausable states have a stage that can be abandoned and redone, or a wait.
func (s State) pausable() bool {
	return s.running() || s == Queued || s == AwaitingReview || s == ReadyToPublish || s == Preflighted || s == Locked
}

func to(s Snapshot, state State, dispatch kernel.StageKind) Result {
	s.State = state
	s.Block = ""
	return Result{Snapshot: s, Dispatch: dispatch}
}

func failFile(s Snapshot, l Limits, stage kernel.StageKind) Result {
	s.FailedStage = stage
	if s.Counters.Reprocessed >= l.MaxReprocess {
		// Reprocessing already used: the file cannot be processed (3.4).
		r := to(s, Unprocessable, kernel.StageNone)
		return r
	}
	r := to(s, Failed, kernel.StageNone)
	r.Exception = exceptions.FileFailed
	return r
}

// Transition is pure: the same (snapshot, event, limits) always gives the same result.
func Transition(s Snapshot, e Event, l Limits) (Result, error) {
	invalid := func() (Result, error) { return Result{}, ErrInvalidTransition }
	if s.State.Terminal() {
		return invalid()
	}
	switch e.Kind {
	case EvStageOutcome:
		return stageOutcome(s, e, l)

	case EvConfirm:
		switch s.State {
		case Preflighted:
			s.Cursor = kernel.StageExtractDetect
			return to(s, Queued, kernel.StageNone), nil
		case Locked:
			r := to(s, Blocked, kernel.StageNone)
			r.Snapshot.Block = BlockPassword
			r.Exception = exceptions.PasswordRequired
			return r, nil
		}
		return invalid()

	case EvDispatch:
		switch s.State {
		case Queued:
			next := s.Cursor
			if next == kernel.StageNone {
				next = kernel.StageExtractDetect
			}
			state, ok := stateOf[next]
			if !ok {
				return invalid()
			}
			return to(s, state, next), nil
		case ReadyToPublish:
			return to(s, Publishing, kernel.StagePublish), nil
		}
		return invalid()

	case EvDecisionsChange:
		switch s.State {
		case AwaitingReview:
			return to(s, Resolving, kernel.StageResolve), nil
		case Exporting, Validating, ReadyToPublish, Resolving:
			// A decision changed under a later stage: redo from resolution.
			s.Cursor = kernel.StageResolve
			return to(s, Queued, kernel.StageNone), nil
		case Blocked, Failed:
			// The file stays stopped, but what it would go on with was made from the old decisions: a retry
			// must not publish that.
			return Transition(s, Event{Kind: EvInvalidate, Stage: kernel.StageResolve}, l)
		}
		return invalid()

	case EvUnlocked:
		if s.State == Locked || (s.State == Blocked && s.Block == BlockPassword) {
			// The decrypted copy is probed again before anything else runs.
			return to(s, Preflighting, kernel.StagePreflight), nil
		}
		return invalid()

	case EvUnblock:
		if s.State == Blocked && (s.Block == BlockResource || s.Block == BlockTarget) {
			cursor := s.Cursor
			if cursor == kernel.StageNone {
				return invalid()
			}
			s.Cursor = cursor
			return to(s, Queued, kernel.StageNone), nil
		}
		return invalid()

	case EvPause:
		if !s.State.pausable() {
			return invalid()
		}
		s.Resume = s.State
		return to(s, Paused, kernel.StageNone), nil

	case EvResume:
		if s.State != Paused || s.Resume == "" {
			return invalid()
		}
		back := s.Resume
		s.Resume = ""
		if stage, ok := stageOf[back]; ok {
			// The abandoned attempt is redone from the stage start (FR-05).
			s.Cursor = stage
			return to(s, Queued, kernel.StageNone), nil
		}
		result := to(s, back, kernel.StageNone)
		if back == Blocked {
			result.Snapshot.Block = s.Block
		}
		return result, nil

	case EvCancel:
		s.Resume = ""
		return to(s, Cancelling, kernel.StageNone), nil

	case EvCancelDone:
		if s.State != Cancelling {
			return invalid()
		}
		return to(s, Cancelled, kernel.StageNone), nil

	case EvReprocess:
		if s.State != Failed || s.Counters.Reprocessed >= l.MaxReprocess || s.FailedStage == kernel.StageNone {
			return invalid()
		}
		s.Counters.Reprocessed++
		s.Cursor = s.FailedStage
		return to(s, Queued, kernel.StageNone), nil

	case EvSkip:
		if s.State != Failed && s.State != AwaitingReview && s.State != Blocked && s.State != Locked {
			return invalid()
		}
		return to(s, Skipped, kernel.StageNone), nil

	case EvInvalidate:
		if _, ok := stateOf[e.Stage]; !ok || e.Stage == kernel.StagePreflight {
			return invalid()
		}
		switch s.State {
		case Preflighting, Preflighted, Locked, Cancelling:
			return invalid()
		case Paused:
			// Stale results must be redone, but a pause is the user's decision:
			// it stays paused and continues from the stale stage on resume.
			s.Cursor = e.Stage
			s.Resume = Queued
			r := Result{Snapshot: s}
			return r, nil
		case Blocked:
			// A password, input or resource block is not cleared by a changed
			// configuration; only the resume point moves back.
			s.Cursor = e.Stage
			return Result{Snapshot: s}, nil
		case Failed:
			// Reprocessing starts at the failed stage; it must not start after
			// a stage whose results are stale.
			if stageRank[e.Stage] < stageRank[s.FailedStage] {
				s.FailedStage = e.Stage
			}
			return Result{Snapshot: s}, nil
		}
		s.Cursor = e.Stage
		return to(s, Queued, kernel.StageNone), nil

	case EvInputChanged:
		if s.State == Cancelling || s.State == Preflighting || s.State == Preflighted || s.State == Locked {
			return invalid()
		}
		if cursor, ok := stageOf[s.State]; ok {
			s.Cursor = cursor
		}
		r := to(s, Blocked, kernel.StageNone)
		r.Snapshot.Block = BlockInputChanged
		return r, nil
	}
	return invalid()
}

func stageOutcome(s Snapshot, e Event, l Limits) (Result, error) {
	out := e.Outcome
	switch s.State {
	case Paused:
		// The attempt was abandoned at a safe boundary; whatever it returned is
		// discarded and the stage is redone on resume.
		return Result{Snapshot: s, Discard: true}, nil
	case Cancelling:
		if out.Kind == sched.Cancelled {
			return to(s, Cancelled, kernel.StageNone), nil
		}
		return Result{Snapshot: s, Discard: true}, nil
	}
	expected, running := stageOf[s.State]
	if !running || e.Stage != expected {
		return Result{}, ErrInvalidTransition // late or foreign result
	}
	if out.Kind == sched.Cancelled {
		// Cancellation without a pause or cancel request is not a defined path.
		return Result{}, ErrInvalidTransition
	}

	switch out.Kind {
	case sched.Blocked, sched.Retry:
		// Resource-class conditions block the file and are not counted as
		// reprocessing; only affected files are re-queued when they clear.
		s.Cursor = expected
		reason := BlockResource
		if out.Kind == sched.Blocked && out.Detail == string(BlockTarget) {
			reason = BlockTarget
		}
		r := to(s, Blocked, kernel.StageNone)
		r.Snapshot.Block = reason
		return r, nil
	case sched.Failed:
		if expected == kernel.StagePreflight {
			return to(s, Unprocessable, kernel.StageNone), nil
		}
		return failFile(s, l, expected), nil
	case sched.NeedsReview:
		if expected != kernel.StageResolve {
			return Result{}, ErrInvalidTransition
		}
		return to(s, AwaitingReview, kernel.StageNone), nil
	case sched.Completed:
	default:
		return Result{}, ErrInvalidTransition
	}

	switch expected {
	case kernel.StagePreflight:
		if out.Detail == DetailPasswordRequired {
			return to(s, Locked, kernel.StageNone), nil
		}
		return to(s, Preflighted, kernel.StageNone), nil
	case kernel.StageExtractDetect:
		return to(s, Resolving, kernel.StageResolve), nil
	case kernel.StageResolve:
		return to(s, Exporting, kernel.StageExport), nil
	case kernel.StageExport:
		return to(s, Validating, kernel.StageValidate), nil
	case kernel.StageValidate:
		return validation(s, validate.Conclusion(out.Detail), l), nil
	case kernel.StagePublish:
		if out.Detail == DetailPartial {
			return to(s, Partial, kernel.StageNone), nil
		}
		return to(s, Completed, kernel.StageNone), nil
	}
	return Result{}, ErrInvalidTransition
}

func validation(s Snapshot, c validate.Conclusion, l Limits) Result {
	switch c {
	case validate.Passed:
		return to(s, ReadyToPublish, kernel.StageNone)
	case validate.NotRun:
		if s.Counters.ValidateReruns < l.MaxValidateReruns {
			s.Counters.ValidateReruns++
			return to(s, Validating, kernel.StageValidate)
		}
	case validate.Widened:
		if s.Counters.Widened < l.MaxWiden {
			s.Counters.Widened++
			return to(s, Exporting, kernel.StageExport)
		}
	case validate.NewFindings:
		return to(s, Resolving, kernel.StageResolve)
	case validate.FailedCorrupt, validate.FailedResidual:
		// fall through to failure below
	}
	// Exhausted budgets, corrupt output, residual that cannot be widened and any
	// unknown conclusion fail the file (MD-07).
	return failFile(s, l, kernel.StageValidate)
}

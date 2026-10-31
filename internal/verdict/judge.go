package verdict

// Facts is everything the verdict needs about one file, collected by jobs from
// the owning modules. verdict imports nothing: it is the one place where
// "published", "complete per policy" and "may be published" are decided, so the
// GUI, CLI, reports and the publish gate cannot disagree (INV-01, AR-10).
type Facts struct {
	Phase Phase

	// Validated is true only when every required stage finished and the latest
	// validation passed for the current candidate and plan (INV-02, INV-04).
	Validated bool
	// InputVerified is true when the complete input digest was computed and
	// still equals the frozen one (FR-08).
	InputVerified bool

	PendingExceptions int  // undecided exceptions, including "found after export"
	UnresolvedGaps    int  // coverage gaps with no removal and no keep decision
	RetainedGaps      int  // gaps the user kept (MD-17), making a partial result
	PartialEligible   bool // both steps of coverage.PartialEligible hold (FR-35)

	// HumanDecided is true if any disposition rests on a person's decision
	// rather than on the frozen policy or a field rule (FR-37).
	HumanDecided bool
	// PluginQualified: every plugin that did a stage has the completion grant of
	// FR-44 (built-in plugins always do).
	PluginQualified bool
	// CategoriesQualified: no category depends only on a third-party rule pack or
	// on a model threshold above its calibrated default (FR-46, MD-12).
	CategoriesQualified bool

	PublishGranted bool // a grant bound to the current disposition key exists
}

// Phase is the coarse position of a file in its life cycle, mapped by jobs
// from its task state. The set is closed; unknown values are treated as not
// complete (MD-07).
type Phase string

const (
	PhaseProcessing     Phase = "processing"
	PhaseAwaitingReview Phase = "awaiting_review"
	PhaseBlocked        Phase = "blocked"
	PhaseFailed         Phase = "failed" // reprocessing still possible
	PhaseUnprocessable  Phase = "unprocessable"
	PhaseSkipped        Phase = "skipped"
	PhaseCancelled      Phase = "cancelled"
	PhaseReady          Phase = "ready_to_publish"
	PhasePublishing     Phase = "publishing"
	PhasePublished      Phase = "published"
)

// Readiness is the publish gate result.
type Readiness string

const (
	ReadyFull    Readiness = "full"
	ReadyPartial Readiness = "partial"
	NotReady     Readiness = "not_ready"
)

// Reason says why a file is not ready. Empty when it is.
type Reason string

const (
	ReasonNotValidated    Reason = "not_validated"
	ReasonInputUnverified Reason = "input_unverified"
	ReasonPending         Reason = "pending_exceptions"
	ReasonGaps            Reason = "coverage_gaps"
	ReasonPartialBlocked  Reason = "partial_not_eligible"
)

// PublishReadiness decides whether the file may be published, and as what.
// A kept gap makes a partial result only when the keep was allowed (FR-35).
func PublishReadiness(f Facts) (Readiness, Reason) {
	switch {
	case !f.Validated:
		return NotReady, ReasonNotValidated
	case !f.InputVerified:
		return NotReady, ReasonInputUnverified
	case f.PendingExceptions > 0:
		return NotReady, ReasonPending
	case f.UnresolvedGaps > 0:
		return NotReady, ReasonGaps
	case f.RetainedGaps > 0 && !f.PartialEligible:
		return NotReady, ReasonPartialBlocked
	case f.RetainedGaps > 0:
		return ReadyPartial, ""
	}
	return ReadyFull, ""
}

// Judge returns the result status of one file.
func Judge(f Facts) Status {
	switch f.Phase {
	case PhaseUnprocessable:
		return Unprocessable
	case PhaseSkipped:
		return Skipped
	case PhaseCancelled:
		return Cancelled
	case PhaseFailed:
		return ProcessingFailed
	case PhaseBlocked:
		return Blocked
	case PhaseAwaitingReview:
		return AwaitingReview
	case PhasePublished:
		ready, _ := PublishReadiness(f)
		switch ready {
		case ReadyPartial:
			return PartialResult
		case ReadyFull:
			// "Completed per policy" needs every condition of requirements 3.4;
			// anything weaker is only "delivered" (FR-44).
			if f.PluginQualified {
				return CompletedPerPolicy
			}
			return Delivered
		}
		// A published file whose facts no longer pass the gate cannot be
		// presented as complete; it stays visible as delivered-but-suspect.
		return Delivered
	case PhaseReady:
		ready, _ := PublishReadiness(f)
		switch {
		case ready == NotReady:
			// The file claims to be ready but the gate disagrees: needs attention,
			// never silently shown as waiting or finished.
			return Blocked
		case !f.PublishGranted:
			return AwaitingPublish
		}
		return Processing
	case PhasePublishing, PhaseProcessing:
		return Processing
	}
	return Processing // unknown phase never reads as finished
}

// UnattendedEligible is true only for a fully publishable file whose every
// decision came from the frozen policy and whose plugins and categories are
// qualified (FR-37, FR-44, FR-46, requirements 6.2). A partial result never is.
func UnattendedEligible(f Facts) bool {
	ready, _ := PublishReadiness(f)
	return ready == ReadyFull && !f.HumanDecided && f.PluginQualified && f.CategoriesQualified
}

// BatchPhase is the aggregate shown for a batch (architecture 6).
type BatchPhase string

const (
	BatchRunning       BatchPhase = "running"
	BatchWaitingUser   BatchPhase = "waiting_user"
	BatchAllDelivered  BatchPhase = "all_delivered"
	BatchEndedWithGaps BatchPhase = "ended_with_undelivered"
	BatchStopped       BatchPhase = "stopped"
)

// Summary counts the members by status for "delivered n of m" (BUX-12).
type Summary struct {
	Total       int
	Counts      map[Status]int
	Delivered   int // completed per policy or delivered
	Phase       BatchPhase
	ShowResults bool // every member ended and nothing awaits review or publish (BUX-16)
}

func deliveredStatus(s Status) bool { return s == CompletedPerPolicy || s == Delivered }

// Summarize aggregates member statuses. An empty batch is never "all
// delivered" (INV-01).
func Summarize(members []Status) Summary {
	s := Summary{Total: len(members), Counts: map[Status]int{}}
	for _, m := range members {
		s.Counts[m]++
		if deliveredStatus(m) {
			s.Delivered++
		}
	}
	c := s.Counts
	running := c[Processing]
	waiting := c[AwaitingReview] + c[AwaitingPublish] + c[Blocked] + c[ProcessingFailed]
	ended := c[CompletedPerPolicy] + c[Delivered] + c[PartialResult] + c[Skipped] + c[Unprocessable]
	s.ShowResults = s.Total > 0 && running == 0 && waiting == 0 && c[Cancelled] == 0 && ended == s.Total
	switch {
	case s.Total == 0:
		s.Phase = BatchEndedWithGaps
	case running > 0:
		s.Phase = BatchRunning
	case waiting > 0:
		s.Phase = BatchWaitingUser
	case c[Cancelled] > 0 && c[Cancelled]+s.Delivered+c[PartialResult]+c[Skipped]+c[Unprocessable] == s.Total && s.Delivered == 0 && c[PartialResult] == 0:
		s.Phase = BatchStopped
	case s.Delivered == s.Total:
		s.Phase = BatchAllDelivered
	default:
		s.Phase = BatchEndedWithGaps
	}
	return s
}

// ExitCode is the CLI exit status of a finished or inspected batch: 0 only when
// every requested file was delivered per policy; skipped, cancelled, failed,
// partial, blocked, waiting or undelivered files all give 2. Codes 1 and 3 are
// decided before a verdict exists.
func ExitCode(members []Status) int {
	if len(members) == 0 {
		return ExitIncomplete
	}
	for _, m := range members {
		if !deliveredStatus(m) {
			return ExitIncomplete
		}
	}
	return ExitAllPublished
}

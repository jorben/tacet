package jobs

import "context"

// State is a file task state (module design 5.3).
type State string

const (
	// Draft states: preflight runs before the batch is confirmed (MD-08).
	Preflighting State = "preflighting"
	Preflighted  State = "preflighted"
	Locked       State = "locked"

	Queued              State = "queued"
	ExtractingDetecting State = "extracting_detecting"
	Resolving           State = "resolving"
	AwaitingReview      State = "awaiting_review"
	Exporting           State = "exporting"
	Validating          State = "validating"
	ReadyToPublish      State = "ready_to_publish"
	Publishing          State = "publishing"
	Blocked             State = "blocked"
	Paused              State = "paused"
	Cancelling          State = "cancelling"
	Failed              State = "failed"

	Completed     State = "completed"
	Partial       State = "partial"
	Skipped       State = "skipped"
	Unprocessable State = "unprocessable"
	Cancelled     State = "cancelled"
)

// Terminal reports whether no further transition can happen.
func (s State) Terminal() bool {
	switch s {
	case Completed, Partial, Skipped, Unprocessable, Cancelled:
		return true
	}
	return false
}

// waitsForPerson reports whether nothing happens to the file until a person acts
// on it (or it has ended). A file ready to publish moves on by itself once its
// batch publishes automatically.
func (s State) waitsForPerson(autoPublish bool) bool {
	switch s {
	case AwaitingReview, Blocked, Failed, Paused, Locked:
		return true
	case ReadyToPublish:
		return !autoPublish
	}
	return s.Terminal()
}

// BatchKind separates the single-file quick path from regular batches. A
// workspace holds at most one unparked unfinished batch of each kind (FR-28).
type BatchKind string

const (
	QuickBatch   BatchKind = "quick"
	RegularBatch BatchKind = "regular"
)

// Mode is the batch processing mode (requirements 3.5.1).
type Mode string

const (
	Guided     Mode = "guided"
	Unattended Mode = "unattended"
	Strict     Mode = "strict"
)

// DraftReadyHandler is told when a quick draft has passed preflight, so the
// app layer can run the same Start use case as a manual start without jobs
// importing app.
type DraftReadyHandler interface {
	DraftReady(ctx context.Context, batchID string)
}

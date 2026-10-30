package verdict

// Status is the result status shown in the GUI, reports and the manifest.
type Status string

const (
	CompletedPerPolicy Status = "completed_per_policy" // only when every condition of requirements 3.4 holds
	Delivered          Status = "delivered"
	PartialResult      Status = "partial"
	AwaitingPublish    Status = "awaiting_publish"
	AwaitingReview     Status = "awaiting_review"
	Blocked            Status = "blocked"
	ProcessingFailed   Status = "failed"
	Unprocessable      Status = "unprocessable"
	Skipped            Status = "skipped"
	Cancelled          Status = "cancelled"
	Processing         Status = "processing"
)

// Exit codes decided here; 1 (usage error or cannot start) and 3 (user
// interrupt) are decided by the CLI before a verdict exists (requirements 3.6).
const (
	ExitAllPublished = 0
	ExitIncomplete   = 2
)

package sched

import (
	"context"

	"github.com/jorben/tacet/internal/kernel"
)

// JobView is what the scheduler knows about a job when estimating a claim.
type JobView struct {
	Attempt  kernel.AttemptRef
	Stage    kernel.StageKind
	Priority kernel.Priority
}

// StageContext carries the attempt, deadline, cancellation and budget
// metering of one stage run, plus the file facts collected by jobs.
type StageContext interface {
	context.Context
	Attempt() kernel.AttemptRef
}

// OutcomeKind is the closed set of stage outcomes (MD-07).
type OutcomeKind string

const (
	Completed   OutcomeKind = "completed"
	NeedsReview OutcomeKind = "needs_review"
	Blocked     OutcomeKind = "blocked"
	Retry       OutcomeKind = "retry"
	Failed      OutcomeKind = "failed"
	Cancelled   OutcomeKind = "cancelled"
)

// StageOutcome is returned by a stage to jobs. Detail is a closed value owned
// by the stage, such as the validation conclusion; jobs treats unknown values
// as failures.
type StageOutcome struct {
	Kind      OutcomeKind
	Detail    string
	Artifacts []kernel.ArtifactRef
	Err       *kernel.ErrorCode
}

// StageRunner is implemented by each stage module and registered at wiring
// time. The scheduler depends on this interface only.
type StageRunner interface {
	Kind() kernel.StageKind
	Claim(j JobView) kernel.ResourceClaim
	Run(ctx StageContext) StageOutcome
}

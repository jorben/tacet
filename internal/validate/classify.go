package validate

import "sort"

// Facts is everything the closed classification of module design 8.2 reads. The
// stage computes them; Classify only orders them.
type Facts struct {
	// Complete: the validation ran to its end (stream completed, completion marker
	// received, counts agree). Anything else is "not run", never "passed".
	Complete bool
	// Reopened: the candidate could be opened again and every part read.
	Reopened bool
	// Mismatches: part reconciliation failures (coverage.Reconcile).
	Mismatches int
	// Unaccounted: approved operations that are neither verified as executed nor
	// left behind as a residual — the output does not match the plan.
	Unaccounted int
	// Residuals: approved operations whose original content is still in the output.
	Residuals int
	// WidenUsed: the file already had its one automatic widening.
	WidenUsed bool
	// CanWiden: plan.Widen succeeded for these residuals.
	CanWiden bool
	// NewFindings: hits and exceptions that exist only in the candidate.
	NewFindings int
}

// Classify returns the first applicable conclusion in the order of the table.
func Classify(f Facts) Conclusion {
	switch {
	case !f.Complete:
		return NotRun
	case !f.Reopened || f.Mismatches > 0 || f.Unaccounted > 0:
		return FailedCorrupt
	case f.Residuals > 0 && !f.WidenUsed && f.CanWiden:
		return Widened
	case f.Residuals > 0:
		return FailedResidual
	case f.NewFindings > 0:
		return NewFindings
	}
	return Passed
}

// Status is the validation state recorded for the candidate (requirements 4.2):
// new findings are not a failed validation, and a widened candidate is validated
// again as a new one.
type Status string

const (
	StatusNotRun Status = "not_run"
	StatusFailed Status = "failed"
	// StatusPassed means passed within the declared scope: the unverified items
	// and the kept gaps remain listed beside it.
	StatusPassed  Status = "passed_within_declared_scope"
	StatusPartial Status = "partial"
)

// StatusOf maps a conclusion to the recorded state. A pass with a kept gap is
// partial, never a full pass.
func StatusOf(c Conclusion, keptGaps bool) Status {
	switch c {
	case FailedCorrupt, Widened, FailedResidual:
		return StatusFailed
	case NewFindings, Passed:
		if keptGaps {
			return StatusPartial
		}
		return StatusPassed
	}
	return StatusNotRun // NotRun, and any value outside the closed set: never a pass
}

// Unverified is something the validation could not check, reported next to a
// pass instead of being folded into it.
type Unverified struct{ Scope, Reason string }

// SortUnverified orders them for a stable record.
func SortUnverified(u []Unverified) {
	sort.Slice(u, func(i, j int) bool {
		if u[i].Scope != u[j].Scope {
			return u[i].Scope < u[j].Scope
		}
		return u[i].Reason < u[j].Reason
	})
}

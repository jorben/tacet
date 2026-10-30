package coverage

// The three coverage dimensions are recorded independently; no single state
// expresses both "inspected" and "kept" (requirements 4.2).

// Inspection is whether the content required by the policy was checked.
type Inspection string

const (
	Inspected    Inspection = "inspected"
	NotInspected Inspection = "not_inspected"
	Unsupported  Inspection = "unsupported"
	InspectFail  Inspection = "failed"
)

// Disposition is what happened to a part in the output.
type Disposition string

const (
	Redacted         Disposition = "redacted"
	Removed          Disposition = "removed"
	RetainedByPolicy Disposition = "retained_by_policy"
	Pending          Disposition = "pending"
)

// Validation is the result of checking a part's disposition in the output.
type Validation string

const (
	PassedWithinScope Validation = "passed_within_declared_scope"
	PartialPass       Validation = "partial"
	ValidationFailed  Validation = "failed"
	NotRun            Validation = "not_run"
)

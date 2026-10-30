package validate

// Conclusion is the closed validation result handed to jobs (module design
// 8.2). Conditions are checked in this order and the first match wins.
type Conclusion string

const (
	NotRun         Conclusion = "not_run"         // incomplete: rerun with the same candidate within budget
	FailedCorrupt  Conclusion = "failed_corrupt"  // damaged output or part reconciliation mismatch
	Widened        Conclusion = "widened"         // residual widened once within its unit or region
	FailedResidual Conclusion = "failed_residual" // residual that cannot be widened
	NewFindings    Conclusion = "new_findings"    // not a failure: resolve again
	Passed         Conclusion = "passed"
)

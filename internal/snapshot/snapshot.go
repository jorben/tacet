package snapshot

// Issue is one finding of the compatibility check, with redacted parameters.
type Issue struct {
	Code   string
	Params map[string]string
}

// CompatReport is the result of checking a run preset before freezing.
// Blocking issues refuse the start; NeedsNarrowing issues need the user to
// confirm a narrower scope first, such as rules-only categories (FR-18).
type CompatReport struct {
	Blocking       []Issue
	NeedsNarrowing []Issue
	Warnings       []Issue
}

// DetectFacet is the detect facet as read by detection. Fields are added
// with the implementation; Digest identifies the frozen content.
type DetectFacet struct {
	Digest string
}

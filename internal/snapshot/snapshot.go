package snapshot

// Issue is one finding of the compatibility check, with redacted parameters.
type Issue struct {
	Code   string
	Params map[string]string
}

// CompatReport is the result of checking a run preset before freezing.
// Blocking issues refuse the start. NeedsNarrowing issues name what is out of the run's
// scope (model_dependent_category: the model-dependent categories of a rules-only preset)
// or does not qualify for unattended processing (unattended_*); they do not refuse the
// start and nothing asks about them here. Only editing the rules inside a rule set asks
// first; a change of a model's mapping saves directly (FR-18).
type CompatReport struct {
	Blocking       []Issue
	NeedsNarrowing []Issue
	Warnings       []Issue
}

// DetectFacet is the detect facet as read by detection. Fields are added
// with the implementation; Digest identifies the frozen content.
type DetectFacet struct {
	Digest  string
	Content DetectContent
}

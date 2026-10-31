package detect

import (
	"context"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/snapshot"
)

// Candidate is a detected span before it is stored as a finding.
type Candidate struct {
	Span     kernel.Span
	Category string
	Source   kernel.FindingSource
	Score    float64 // model score; not comparable across models (FR-12)
	LowConf  bool    // below the model's calibrated value (FR-21)
	// RuleID names the rule or dictionary entry set that matched; empty for models.
	RuleID string
	// ValidatorMismatch marks a "suspect" rule hit whose deterministic check
	// failed; policy turns it into an exception rather than a silent drop.
	ValidatorMismatch bool
}

// Detector runs rules and models over text units. Any source failing fails
// the call; partial results are never returned (INV-02).
type Detector interface {
	DetectUnits(ctx context.Context, units []kernel.ArtifactRef, facet snapshot.DetectFacet) ([]Candidate, error)
}

package coverage

import "errors"

var ErrInventoryIncomplete = errors.New("INVENTORY_INCOMPLETE")

// GapKind equals the exception type code the gap becomes (architecture 5.2).
// coverage cannot import exceptions; the policy layer maps by this string.
type GapKind string

const (
	GapScanPage        GapKind = "scan_page"
	GapImageWithText   GapKind = "image_with_text"
	GapEmbeddedObject  GapKind = "embedded_object"
	GapOutlinedText    GapKind = "outlined_text"
	GapUninspectedPart GapKind = "uninspected_part"
)

// ContentClass is what the format plugin says the part is, for naming a gap.
type ContentClass string

const (
	ClassText     ContentClass = "text"
	ClassScanPage ContentClass = "scan_page"
	ClassImage    ContentClass = "image"
	ClassEmbedded ContentClass = "embedded"
	ClassOutlined ContentClass = "outlined_text"
	ClassOther    ContentClass = "other"
)

// Part is one row of the coverage ledger. The three dimensions are separate;
// none implies another (requirements 4.2).
type Part struct {
	ID          string
	Class       ContentClass
	Required    bool // in the policy's must-check scope for the enabled categories
	Known       bool // false: unknown extension or unresolved content
	Bounded     bool // the unchecked range is a registered part or a region of known extent
	Inspection  Inspection
	Disposition Disposition
	Validation  Validation
}

// Gap is a part that makes the file's coverage incomplete.
type Gap struct {
	PartID  string
	Kind    GapKind
	Bounded bool
}

// Report is the ledger summary used by policy, verdict and reports.
type Report struct {
	Unresolved []Gap // no removal and no keep decision yet
	Retained   []Gap // kept by decision: still a gap, never "inspected" (INV-01)
	Resolved   int   // gaps closed by redaction or removal; original inspection state kept
	OutOfScope int   // known, not required, not inspected: listed, not claimed
}

func kindOf(c ContentClass) GapKind {
	switch c {
	case ClassScanPage:
		return GapScanPage
	case ClassImage:
		return GapImageWithText
	case ClassEmbedded:
		return GapEmbeddedObject
	case ClassOutlined:
		return GapOutlinedText
	}
	return GapUninspectedPart
}

// IsGap decides one part (pure). Unknown or opaque content is a gap even when
// nothing refers to it (4.2); a removed or redacted part is no longer exposed
// but keeps its original inspection state in the ledger.
func IsGap(p Part) (gap bool, resolved bool) {
	exposed := p.Disposition != Removed && p.Disposition != Redacted
	switch {
	case !p.Known:
		return true, !exposed
	case p.Required && p.Inspection != Inspected:
		return true, !exposed
	}
	return false, false
}

// Assess builds the ledger report. An incomplete inventory is a failure of the
// whole stage, never a gap list (INV-02).
func Assess(parts []Part, inventoryComplete bool) (Report, error) {
	if !inventoryComplete {
		return Report{}, ErrInventoryIncomplete
	}
	var r Report
	for _, p := range parts {
		gap, resolved := IsGap(p)
		switch {
		case !gap:
			if !p.Required && p.Known && p.Inspection != Inspected {
				r.OutOfScope++
			}
		case resolved:
			r.Resolved++
		case p.Disposition == RetainedByPolicy:
			r.Retained = append(r.Retained, Gap{p.ID, kindOf(p.Class), p.Bounded})
		default:
			r.Unresolved = append(r.Unresolved, Gap{p.ID, kindOf(p.Class), p.Bounded})
		}
	}
	return r, nil
}

// PartialBeforeExport is step one of partial eligibility: every gap that could
// stay in the output has an explicit extent, so the report can say what was
// not checked. It decides whether "keep" may be offered (MD-17).
func PartialBeforeExport(parts []Part, inventoryComplete bool) bool {
	if !inventoryComplete {
		return false
	}
	for _, p := range parts {
		if gap, resolved := IsGap(p); gap && !resolved && !p.Bounded {
			return false
		}
	}
	return true
}

// PostValidation are the facts proven after the candidate was validated.
type PostValidation struct {
	FileBoundaryValid bool // the output is a well-formed file of the declared type
	StructureValid    bool
	ApprovedOpsEffect bool // every approved operation took effect and none left residue
}

// PartialEligible joins both steps; both must hold before a partial result can
// be published (FR-35). The first step alone only governs the choice offered.
func PartialEligible(beforeExport bool, v PostValidation) bool {
	return beforeExport && v.FileBoundaryValid && v.StructureValid && v.ApprovedOpsEffect
}

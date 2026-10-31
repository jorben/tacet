package exceptions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
)

var (
	ErrUnknownType       = errors.New("EXCEPTION_TYPE_UNKNOWN")
	ErrNotPluginReported = errors.New("EXCEPTION_NOT_PLUGIN_REPORTABLE")
)

// ActionKind is a decision the user can take. Plugins never define actions;
// the kernel gives them from the type table and the frozen policy (5.2).
type ActionKind string

const (
	Unlock        ActionKind = "unlock"
	Skip          ActionKind = "skip"
	Reprocess     ActionKind = "reprocess"
	ConfirmExport ActionKind = "confirm_export"
	Redact        ActionKind = "redact" // "涂抹"
	Keep          ActionKind = "keep"   // "保留"
)

// Consequence is what the user is told keep or skip leads to.
type Consequence string

const (
	NoConsequence      Consequence = ""
	PartialResult      Consequence = "partial_result"  // keep leaves unchecked content (MD-17)
	ManualConfirmation Consequence = "manual_decision" // outlined text is counted as human decided (INV-01)
	Undelivered        Consequence = "undelivered"     // skip counts toward the undelivered scope
)

// UnavailableReason explains a disabled action.
type UnavailableReason string

const (
	ReasonPartialNotEligible UnavailableReason = "partial_not_eligible"
	ReasonReprocessUsed      UnavailableReason = "reprocess_used"
)

// Action is one offered decision.
type Action struct {
	Kind        ActionKind
	Available   bool
	Reason      UnavailableReason
	Consequence Consequence
	// NeedsRelocation: redaction proceeds only after the user relocates the
	// selection in the original text or authorizes a reliable larger range.
	NeedsRelocation bool
	// BulkNeedsConfirm: applying this to a whole group first shows the scope
	// summary and its consequence (5.2).
	BulkNeedsConfirm bool
}

// Context carries the facts that change what is offered.
type Context struct {
	// PartialEligible is the first step of coverage.PartialEligible: every
	// unchecked range is a known part or region (4.2, MD-17).
	PartialEligible bool
	// ReprocessUsed is true once the file's single reprocess has been spent.
	ReprocessUsed bool
	// PolicyAllowsGroup is the frozen policy's answer for ByPolicy types.
	PolicyAllowsGroup bool
}

// Actions is pure: the same type and context always give the same offer.
func Actions(t Type, c Context) ([]Action, error) {
	info, ok := Table[t]
	if !ok {
		return nil, ErrUnknownType
	}
	skip := Action{Kind: Skip, Available: true, Consequence: Undelivered, BulkNeedsConfirm: true}
	switch t {
	case PasswordRequired:
		return []Action{{Kind: Unlock, Available: true}, skip}, nil
	case FileFailed:
		re := Action{Kind: Reprocess, Available: !c.ReprocessUsed}
		if c.ReprocessUsed {
			re.Reason = ReasonReprocessUsed
		}
		return []Action{re, skip}, nil
	case StrictConfirm:
		return []Action{{Kind: ConfirmExport, Available: true}, skip}, nil
	}
	redact := Action{Kind: Redact, Available: true, NeedsRelocation: t == UnreliableLocation}
	keep := Action{Kind: Keep, Available: true, BulkNeedsConfirm: true}
	switch {
	case info.KeepIsPartial:
		keep.Consequence = PartialResult
		if !c.PartialEligible {
			keep.Available = false
			keep.Reason = ReasonPartialNotEligible
		}
	case t == OutlinedText:
		keep.Consequence = ManualConfirmation
	}
	return []Action{redact, keep}, nil
}

// CanDecideGroup says whether one decision may cover a whole group.
func CanDecideGroup(t Type, c Context) (bool, error) {
	info, ok := Table[t]
	if !ok {
		return false, ErrUnknownType
	}
	switch info.Group {
	case Groupable:
		return true, nil
	case ByPolicy:
		return c.PolicyAllowsGroup, nil
	}
	return false, nil
}

// ValidatePluginReport checks a code reported by a plugin: it must exist and
// be one the table lets plugins report (MD-07, protocol error otherwise).
func ValidatePluginReport(code string) (Type, error) {
	info, ok := Table[Type(code)]
	if !ok {
		return "", ErrUnknownType
	}
	if !info.PluginReportable {
		return "", ErrNotPluginReported
	}
	return Type(code), nil
}

// ItemID is the stable identity of an exception across re-resolution: the same
// type and scope key give the same id, so decisions and review position stay
// attached (Sync, module design 7.2). The scope key must not contain content.
func ItemID(t Type, scopeKey string) string {
	sum := sha256.Sum256([]byte(string(t) + "\x00" + scopeKey))
	return "ex_" + hex.EncodeToString(sum[:12])
}

// ItemIDFor is the stored identity of an exception: ItemID qualified by the job,
// because the same scope key (a part id, a position) recurs in every job and the
// id is a primary key of the workspace.
func ItemIDFor(job string, t Type, scopeKey string) string {
	return ItemID(t, job+"\x00"+scopeKey)
}

// Item is the minimum needed for aggregation.
type Item struct {
	Type     Type
	Reason   string // coarse cause or category, never document content
	FileID   string
	Resolved bool
}

// Group is a row of the exception queue: one cause, not one row per item.
type Group struct {
	Type   Type
	Reason string
	Items  int
	Files  int
}

// Aggregate groups open items by type and reason (BUX-03). Resolved items do
// not count; the result order is stable.
func Aggregate(items []Item) []Group {
	type key struct {
		t Type
		r string
	}
	counts := map[key]int{}
	files := map[key]map[string]bool{}
	for _, it := range items {
		if it.Resolved {
			continue
		}
		k := key{it.Type, it.Reason}
		counts[k]++
		if files[k] == nil {
			files[k] = map[string]bool{}
		}
		files[k][it.FileID] = true
	}
	out := make([]Group, 0, len(counts))
	for k, n := range counts {
		out = append(out, Group{Type: k.t, Reason: k.r, Items: n, Files: len(files[k])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

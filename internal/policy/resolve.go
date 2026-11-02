package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/kernel"
)

// EngineVersion is part of the resolution key; bump it when Resolve changes.
const EngineVersion = "2" // 2: hand marks of a category without action are placeholders

// Action is the handling of a target. Strength orders removal actions when
// overlapping targets merge: delete > placeholder > mask (FR-13).
type Action string

const (
	Delete      Action = "delete"
	Placeholder Action = "placeholder"
	Mask        Action = "mask"
	Keep        Action = "keep"
	RemovePart  Action = "remove_part" // whole-part removal for a coverage gap
)

func strength(a Action) int {
	switch a {
	case Delete:
		return 3
	case Placeholder:
		return 2
	case Mask:
		return 1
	}
	return 0
}

// Basis says why a disposition exists (module design 7.1 output).
type Basis string

const (
	AutoPolicy Basis = "auto_policy"
	FieldRule  Basis = "field_rule"
	HumanItem  Basis = "human_item"
	HumanBulk  Basis = "human_bulk"
)

// rank is the decision priority of FR-14: single human decision > bulk human
// decision > explicit policy rule > automatic detection suggestion.
func rank(b Basis, explicit bool) int {
	switch b {
	case HumanItem:
		return 4
	case HumanBulk:
		return 3
	}
	if explicit || b == FieldRule {
		return 2
	}
	return 1
}

// Policy is the dispose facet content, as frozen in the run snapshot.
type Policy struct {
	CategoryAction map[string]Action
	// MinConfidence applies to model candidates only (MD-12); 0 disables it.
	MinConfidence float64
	// Calibrated is the model's calibrated default; below it a candidate is
	// marked low-confidence but is still handled per its category.
	Calibrated           float64
	HumanReview          map[string]bool // categories that need a person (strict mode)
	ReviewLowConfidence  bool
	StrictFile           bool // one strict_confirm per file
	TypeChangeAuthorized bool
	AutoMismatch         bool            // validator mismatches are handled by the policy itself
	UnsupportedActions   map[Action]bool // actions this format cannot perform
	GapRemove            bool            // default for coverage gaps: remove the part
}

// Candidate is one finding as the policy sees it. Spans are code points of Unit.
type Candidate struct {
	ID                string
	Unit              string
	Start, End        int
	Category          string
	Source            kernel.FindingSource
	Score             float64
	KeepByPolicy      bool // matched a keep exception of the policy
	ValidatorMismatch bool
	Unreliable        bool // source mapping marked unreliable (INV-09)
	NeedsTypeChange   bool // structured value would change type or key
}

// Decision is a committed human decision on a target.
type Decision struct {
	Keep bool // false: redact, i.e. apply the category action
	Bulk bool
}

// Input is everything Resolve reads. It has no clock, store or I/O.
type Input struct {
	Policy       Policy
	Candidates   []Candidate
	Decisions    map[string]Decision // by candidate id
	Gaps         []coverage.Gap
	GapDecisions map[string]Decision // by part id
}

// Disposition is one resolved target (or merged group of targets).
type Disposition struct {
	ID         string // id of the first merged target, or the part id
	Unit       string
	Start, End int
	Category   string
	Action     Action
	Basis      Basis
	Merged     []string
}

// Draft is an exception the resolution cannot decide; exceptions.Sync stores it.
type Draft struct {
	Type     exceptions.Type
	ScopeKey string // no content, only ids and positions
	Reason   string
}

// Stats counts by decision source (INV-01) and the MD-12 unprocessed numbers.
type Stats struct {
	Auto, HumanItem, HumanBulk int
	BelowMinConfidence         int
	LowConfidence              int
}

type Output struct {
	Dispositions []Disposition
	Drafts       []Draft
	Stats        Stats
}

type target struct {
	c      Candidate
	action Action
	basis  Basis
	rank   int
}

// Resolve is pure: identical input gives identical output (MD-02).
func Resolve(in Input) Output {
	p := in.Policy
	var out Output
	cands := append([]Candidate(nil), in.Candidates...)
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.ID < b.ID
	})
	draft := func(t exceptions.Type, scope, reason string) {
		out.Drafts = append(out.Drafts, Draft{t, scope, reason})
	}
	scopeOf := func(c Candidate) string { return scopeKey(c.Unit, c.Start, c.End) }

	var targets []target
	for _, c := range cands {
		dec, decided := in.Decisions[c.ID]
		redactedByHuman := decided && !dec.Keep
		isModel := c.Source == kernel.SourceModel
		// 1. Below the minimum: kept in the store, not a target, counted (MD-12).
		// A person's decision to redact it overrides.
		if isModel && p.MinConfidence > 0 && c.Score < p.MinConfidence && !redactedByHuman {
			out.Stats.BelowMinConfidence++
			continue
		}
		if c.Unreliable {
			draft(exceptions.UnreliableLocation, scopeOf(c), "unreliable_location")
			continue
		}
		if c.ValidatorMismatch && !p.AutoMismatch && !decided {
			draft(exceptions.ValidatorMismatch, scopeOf(c), "validator_mismatch")
			continue
		}
		if c.NeedsTypeChange && !p.TypeChangeAuthorized && !redactedByHuman {
			draft(exceptions.TypeChange, scopeOf(c), "type_change")
			continue
		}
		if isModel && p.Calibrated > 0 && c.Score < p.Calibrated {
			out.Stats.LowConfidence++
			if p.ReviewLowConfidence && !decided {
				draft(exceptions.PolicyReview, scopeOf(c), "low_confidence")
				continue
			}
		}
		if p.HumanReview[c.Category] && !decided {
			draft(exceptions.PolicyReview, scopeOf(c), "human_review_required")
			continue
		}
		t := target{c: c}
		switch {
		case decided:
			t.basis = HumanItem
			if dec.Bulk {
				t.basis = HumanBulk
			}
			t.rank = rank(t.basis, false)
			if dec.Keep {
				t.action = Keep
			} else if a, ok := categoryAction(p, c); ok && a != Keep {
				t.action = a
			} else {
				t.action = Delete // a person chose to redact a category with no removal action
			}
		case c.KeepByPolicy:
			t.action, t.basis, t.rank = Keep, AutoPolicy, rank(AutoPolicy, true)
		default:
			a, ok := categoryAction(p, c)
			if !ok {
				draft(exceptions.PolicyReview, scopeOf(c), "no_action_for_category")
				continue
			}
			t.action, t.basis = a, AutoPolicy
			if c.Source == kernel.SourceFieldRule {
				t.basis = FieldRule
			}
			t.rank = rank(t.basis, false)
		}
		if t.action != Keep && p.UnsupportedActions[t.action] {
			draft(exceptions.PolicyReview, scopeOf(c), "format_unsupported_action")
			continue
		}
		targets = append(targets, t)
	}

	// 4. Overlap merge per unit (FR-13); targets are sorted by unit and start.
	for i := 0; i < len(targets); {
		j, end := i+1, targets[i].c.End
		for j < len(targets) && targets[j].c.Unit == targets[i].c.Unit && targets[j].c.Start <= end {
			if targets[j].c.End > end {
				end = targets[j].c.End
			}
			j++
		}
		cluster := targets[i:j]
		i = j
		d, conflict := mergeCluster(cluster, end)
		if conflict {
			c := cluster[0].c
			draft(exceptions.PolicyReview, fmt.Sprintf("%s:%d-%d", c.Unit, c.Start, end), "conflict")
			continue
		}
		out.Dispositions = append(out.Dispositions, d)
	}

	// 8. Coverage gaps: default handling or an exception of the gap's kind.
	for _, g := range in.Gaps {
		dec, decided := in.GapDecisions[g.PartID]
		switch {
		case decided && dec.Keep:
			out.Dispositions = append(out.Dispositions, Disposition{ID: g.PartID, Action: Keep, Basis: humanBasis(dec), Category: string(g.Kind)})
		case decided:
			out.Dispositions = append(out.Dispositions, Disposition{ID: g.PartID, Action: RemovePart, Basis: humanBasis(dec), Category: string(g.Kind)})
		case p.GapRemove:
			out.Dispositions = append(out.Dispositions, Disposition{ID: g.PartID, Action: RemovePart, Basis: AutoPolicy, Category: string(g.Kind)})
		default:
			draft(exceptions.Type(g.Kind), "part:"+g.PartID, string(g.Kind))
		}
	}
	// 9. Strict mode: one confirmation per file.
	if p.StrictFile {
		draft(exceptions.StrictConfirm, "file", "strict_confirm")
	}

	for _, d := range out.Dispositions {
		switch d.Basis {
		case HumanItem:
			out.Stats.HumanItem++
		case HumanBulk:
			out.Stats.HumanBulk++
		default:
			out.Stats.Auto++
		}
	}
	sort.SliceStable(out.Drafts, func(i, j int) bool {
		a, b := out.Drafts[i], out.Drafts[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ScopeKey < b.ScopeKey
	})
	return out
}

// categoryAction is the scheme's action for the candidate's category. A hand mark of a category the scheme
// has no action for (any category: no detection source, hence often no scheme entry) is a placeholder: the
// mark itself asks for removal, the placeholder names what was removed.
func categoryAction(p Policy, c Candidate) (Action, bool) {
	if a, ok := p.CategoryAction[c.Category]; ok {
		return a, true
	}
	if c.Source == kernel.SourceManual {
		return Placeholder, true
	}
	return "", false
}

func humanBasis(d Decision) Basis {
	if d.Bulk {
		return HumanBulk
	}
	return HumanItem
}

// mergeCluster resolves overlapping or touching targets of one unit. It returns
// conflict=true when no safe single handling exists; nothing is dropped silently.
func mergeCluster(cluster []target, end int) (Disposition, bool) {
	start := cluster[0].c.Start
	ids := make([]string, len(cluster))
	best := cluster[0]
	var keeps, removals int
	placeholderCats := map[string]bool{}
	hasDelete := false
	for k, t := range cluster {
		ids[k] = t.c.ID
		if t.rank > best.rank {
			best = t
		}
		if t.action == Keep {
			keeps++
			continue
		}
		removals++
		hasDelete = hasDelete || t.action == Delete
		if t.action == Placeholder {
			placeholderCats[t.c.Category] = true
		}
	}
	d := Disposition{ID: ids[0], Unit: cluster[0].c.Unit, Start: start, End: end, Category: best.c.Category, Basis: best.basis, Merged: ids}
	if len(cluster) == 1 {
		d.Action, d.Merged = cluster[0].action, nil
		return d, false
	}
	switch {
	case removals == 0:
		d.Action = Keep
		return d, false
	case keeps == 0:
		if !hasDelete && len(placeholderCats) > 1 {
			return Disposition{}, true // two incompatible replacements
		}
		a := Mask
		for _, t := range cluster {
			if strength(t.action) > strength(a) {
				a = t.action
			}
		}
		d.Action = a
		// Category and basis come from the strongest action, then priority.
		for _, t := range cluster {
			if t.action == a && t.rank >= best.rank {
				best = t
			}
		}
		d.Category, d.Basis = best.c.Category, best.basis
		return d, false
	}
	// Keep overlapping removal: a strictly higher-priority decision that covers
	// the whole cluster wins; anything else is a conflict for a person.
	if best.c.Start != start || best.c.End != end {
		return Disposition{}, true
	}
	for _, t := range cluster {
		if (t.action == Keep) != (best.action == Keep) && t.rank >= best.rank {
			return Disposition{}, true
		}
	}
	d.Action = best.action
	return d, false
}

// ResolutionKey identifies a resolution: detection result version, dispose
// facet digest, decision version and engine version (module design 7.1).
func ResolutionKey(detectVersion uint64, disposeDigest string, decisionVersion uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d\x00%s", detectVersion, disposeDigest, decisionVersion, EngineVersion)))
	return hex.EncodeToString(sum[:16])
}

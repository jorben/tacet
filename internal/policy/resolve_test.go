package policy

import (
	"reflect"
	"testing"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/kernel"
)

func base() Policy {
	return Policy{
		CategoryAction: map[string]Action{"phone": Delete, "person": Placeholder, "email": Mask, "date": Keep},
		MinConfidence:  0.8, Calibrated: 0.9,
	}
}

func cand(id string, start, end int, cat string, src kernel.FindingSource, score float64) Candidate {
	return Candidate{ID: id, Unit: "u1", Start: start, End: end, Category: cat, Source: src, Score: score}
}

func resolve(p Policy, cs ...Candidate) Output { return Resolve(Input{Policy: p, Candidates: cs}) }

func TestMinimumConfidenceAppliesToModelCandidatesOnly(t *testing.T) {
	out := Resolve(Input{Policy: base(), Candidates: []Candidate{
		cand("m-low", 0, 3, "person", kernel.SourceModel, 0.5),
		cand("m-ok", 10, 13, "person", kernel.SourceModel, 0.95),
		cand("rule", 20, 31, "phone", kernel.SourceRule, 0.1),
		cand("dict", 40, 44, "person", kernel.SourceDictionary, 0),
		cand("manual", 50, 52, "person", kernel.SourceManual, 0),
		cand("term", 60, 64, "person", kernel.SourceUserTerm, 0),
		cand("field", 70, 80, "phone", kernel.SourceFieldRule, 0),
	}})
	got := map[string]bool{}
	for _, d := range out.Dispositions {
		got[d.ID] = true
	}
	for _, id := range []string{"m-ok", "rule", "dict", "manual", "term", "field"} {
		if !got[id] {
			t.Errorf("%s must be handled", id)
		}
	}
	if got["m-low"] || len(out.Dispositions) != 6 {
		t.Fatalf("below-threshold model candidate was handled: %+v", out.Dispositions)
	}
	if out.Stats.BelowMinConfidence != 1 || len(out.Drafts) != 0 {
		t.Fatalf("it is counted, not an exception: %+v %+v", out.Stats, out.Drafts)
	}
}

func TestHumanRedactionOverridesTheThresholdAndIsCountedAsHuman(t *testing.T) {
	in := Input{Policy: base(),
		Candidates: []Candidate{cand("low", 0, 3, "person", kernel.SourceModel, 0.4), cand("low2", 10, 13, "person", kernel.SourceModel, 0.4)},
		Decisions:  map[string]Decision{"low": {}, "low2": {Bulk: true}}}
	out := Resolve(in)
	if len(out.Dispositions) != 2 || out.Dispositions[0].Basis != HumanItem || out.Dispositions[1].Basis != HumanBulk {
		t.Fatalf("%+v", out.Dispositions)
	}
	if out.Stats.HumanItem != 1 || out.Stats.HumanBulk != 1 || out.Stats.Auto != 0 || out.Stats.BelowMinConfidence != 0 {
		t.Fatalf("%+v", out.Stats)
	}
	// A keep decision on a below-threshold candidate changes nothing.
	in.Decisions = map[string]Decision{"low": {Keep: true}}
	if out = Resolve(in); len(out.Dispositions) != 0 || out.Stats.BelowMinConfidence != 2 {
		t.Fatalf("%+v %+v", out.Dispositions, out.Stats)
	}
}

func TestLowConfidenceIsMarkedButHandledUnlessStrictReview(t *testing.T) {
	c := cand("a", 0, 3, "person", kernel.SourceModel, 0.85) // above 0.8, below calibrated 0.9
	out := resolve(base(), c)
	if len(out.Dispositions) != 1 || out.Stats.LowConfidence != 1 {
		t.Fatalf("%+v", out)
	}
	p := base()
	p.ReviewLowConfidence = true
	out = resolve(p, c)
	if len(out.Dispositions) != 0 || len(out.Drafts) != 1 || out.Drafts[0].Type != exceptions.PolicyReview || out.Drafts[0].Reason != "low_confidence" {
		t.Fatalf("%+v", out)
	}
}

func TestDecisionPriorityHumanOverPolicyOverAuto(t *testing.T) {
	keepByPolicy := cand("k", 0, 5, "phone", kernel.SourceRule, 0)
	keepByPolicy.KeepByPolicy = true
	out := resolve(base(), keepByPolicy)
	if out.Dispositions[0].Action != Keep || out.Dispositions[0].Basis != AutoPolicy {
		t.Fatalf("keep exception: %+v", out.Dispositions)
	}
	// A single human redaction beats the policy keep exception.
	out = Resolve(Input{Policy: base(), Candidates: []Candidate{keepByPolicy}, Decisions: map[string]Decision{"k": {}}})
	if out.Dispositions[0].Action != Delete || out.Dispositions[0].Basis != HumanItem {
		t.Fatalf("human over policy: %+v", out.Dispositions)
	}
	// Field rule is reported as its own basis.
	out = resolve(base(), cand("f", 0, 5, "phone", kernel.SourceFieldRule, 0))
	if out.Dispositions[0].Basis != FieldRule {
		t.Fatalf("%+v", out.Dispositions)
	}
}

func TestOverlapAndAdjacentRemovalsMergeToUnionWithStrongestAction(t *testing.T) {
	out := resolve(base(),
		cand("a", 0, 5, "email", kernel.SourceRule, 0),      // mask
		cand("b", 4, 9, "person", kernel.SourceModel, 0.95), // placeholder
		cand("c", 9, 12, "phone", kernel.SourceRule, 0),     // delete, adjacent
		cand("far", 20, 22, "email", kernel.SourceRule, 0))
	if len(out.Dispositions) != 2 {
		t.Fatalf("%+v", out.Dispositions)
	}
	d := out.Dispositions[0]
	if d.Start != 0 || d.End != 12 || d.Action != Delete || d.Category != "phone" || !reflect.DeepEqual(d.Merged, []string{"a", "b", "c"}) {
		t.Fatalf("merged: %+v", d)
	}
	if out.Dispositions[1].ID != "far" || out.Dispositions[1].Merged != nil {
		t.Fatalf("%+v", out.Dispositions[1])
	}
}

func TestIncompatibleReplacementsBecomeAConflictNotASilentChoice(t *testing.T) {
	p := base()
	p.CategoryAction["address"] = Placeholder
	out := resolve(p, cand("a", 0, 5, "person", kernel.SourceRule, 0), cand("b", 3, 8, "address", kernel.SourceRule, 0))
	if len(out.Dispositions) != 0 || len(out.Drafts) != 1 || out.Drafts[0].Reason != "conflict" || out.Drafts[0].Type != exceptions.PolicyReview {
		t.Fatalf("%+v", out)
	}
	// A delete in the same cluster is unambiguous.
	out = resolve(p, cand("a", 0, 5, "person", kernel.SourceRule, 0), cand("b", 3, 8, "address", kernel.SourceRule, 0), cand("c", 4, 6, "phone", kernel.SourceRule, 0))
	if len(out.Dispositions) != 1 || out.Dispositions[0].Action != Delete || len(out.Drafts) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestKeepOverlappingRemovalNeedsAHigherCoveringDecision(t *testing.T) {
	remove := cand("r", 0, 10, "phone", kernel.SourceRule, 0)
	keep := cand("k", 3, 6, "phone", kernel.SourceRule, 0)
	keep.KeepByPolicy = true
	// Partial overlap, keep not covering: conflict.
	if out := resolve(base(), remove, keep); len(out.Dispositions) != 0 || out.Drafts[0].Reason != "conflict" {
		t.Fatalf("%+v", out)
	}
	// An explicit keep exception that covers the span beats an automatic removal (FR-14).
	keep.Start, keep.End = 0, 10
	if out := resolve(base(), remove, keep); len(out.Dispositions) != 1 || out.Dispositions[0].Action != Keep || len(out.Drafts) != 0 {
		t.Fatalf("policy keep over auto removal: %+v", resolve(base(), remove, keep))
	}
	// Equal priority (field rule removal vs keep exception) must not be decided silently.
	tied := remove
	tied.Source = kernel.SourceFieldRule
	if out := resolve(base(), tied, keep); len(out.Dispositions) != 0 || len(out.Drafts) != 1 || out.Drafts[0].Reason != "conflict" {
		t.Fatalf("tie: %+v", out)
	}
	// A covering human keep wins over the automatic removal.
	out := Resolve(Input{Policy: base(), Candidates: []Candidate{remove, keep}, Decisions: map[string]Decision{"k": {Keep: true}}})
	if len(out.Dispositions) != 1 || out.Dispositions[0].Action != Keep || out.Dispositions[0].Basis != HumanItem || len(out.Drafts) != 0 {
		t.Fatalf("%+v", out)
	}
	// A human keep that does not cover the removal is a conflict, not a drop.
	keep.Start, keep.End = 3, 6
	out = Resolve(Input{Policy: base(), Candidates: []Candidate{remove, keep}, Decisions: map[string]Decision{"k": {Keep: true}}})
	if len(out.Dispositions) != 0 || len(out.Drafts) != 1 {
		t.Fatalf("%+v", out)
	}
}

func TestExceptionDraftsForUnsafeOrUndecidableTargets(t *testing.T) {
	p := base()
	p.HumanReview = map[string]bool{"person": true}
	p.UnsupportedActions = map[Action]bool{Mask: true}
	unreliable := cand("u", 0, 3, "phone", kernel.SourceRule, 0)
	unreliable.Unreliable = true
	mismatch := cand("v", 10, 28, "phone", kernel.SourceRule, 0)
	mismatch.ValidatorMismatch = true
	typed := cand("t", 30, 35, "phone", kernel.SourceFieldRule, 0)
	typed.NeedsTypeChange = true
	out := resolve(p, unreliable, mismatch, typed,
		cand("h", 40, 43, "person", kernel.SourceModel, 0.95),
		cand("m", 50, 55, "email", kernel.SourceRule, 0),
		cand("n", 60, 65, "unknown_category", kernel.SourceRule, 0))
	reasons := map[string]exceptions.Type{}
	for _, d := range out.Drafts {
		reasons[d.Reason] = d.Type
	}
	want := map[string]exceptions.Type{
		"unreliable_location": exceptions.UnreliableLocation, "validator_mismatch": exceptions.ValidatorMismatch,
		"type_change": exceptions.TypeChange, "human_review_required": exceptions.PolicyReview,
		"format_unsupported_action": exceptions.PolicyReview, "no_action_for_category": exceptions.PolicyReview,
	}
	if !reflect.DeepEqual(reasons, want) || len(out.Dispositions) != 0 {
		t.Fatalf("drafts %v dispositions %+v", reasons, out.Dispositions)
	}
	// Authorizations remove the corresponding drafts.
	p.TypeChangeAuthorized, p.AutoMismatch = true, true
	if out = resolve(p, mismatch, typed); len(out.Dispositions) != 2 || len(out.Drafts) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestGapsAreRemovedByDefaultOrBecomeTypedExceptions(t *testing.T) {
	gaps := []coverage.Gap{{PartID: "p1", Kind: coverage.GapScanPage}, {PartID: "p2", Kind: coverage.GapEmbeddedObject}, {PartID: "p3", Kind: coverage.GapImageWithText}}
	out := Resolve(Input{Policy: base(), Gaps: gaps, GapDecisions: map[string]Decision{"p2": {Keep: true}, "p3": {Bulk: true}}})
	if len(out.Drafts) != 1 || out.Drafts[0].Type != exceptions.ScanPage || out.Drafts[0].ScopeKey != "part:p1" {
		t.Fatalf("%+v", out.Drafts)
	}
	byID := map[string]Disposition{}
	for _, d := range out.Dispositions {
		byID[d.ID] = d
	}
	if byID["p2"].Action != Keep || byID["p3"].Action != RemovePart || byID["p3"].Basis != HumanBulk {
		t.Fatalf("%+v", byID)
	}
	p := base()
	p.GapRemove = true
	out = Resolve(Input{Policy: p, Gaps: gaps})
	if len(out.Drafts) != 0 || len(out.Dispositions) != 3 || out.Stats.Auto != 3 {
		t.Fatalf("%+v", out)
	}
}

func TestStrictFileAddsOneConfirmation(t *testing.T) {
	p := base()
	p.StrictFile = true
	out := resolve(p, cand("a", 0, 3, "phone", kernel.SourceRule, 0))
	if len(out.Drafts) != 1 || out.Drafts[0].Type != exceptions.StrictConfirm || len(out.Dispositions) != 1 {
		t.Fatalf("%+v", out)
	}
}

func TestResolveIsDeterministicIndependentOfInputOrderAndDoesNotMutate(t *testing.T) {
	cs := []Candidate{cand("b", 20, 25, "phone", kernel.SourceRule, 0), cand("a", 0, 5, "email", kernel.SourceRule, 0), cand("c", 3, 8, "person", kernel.SourceModel, 0.95)}
	rev := []Candidate{cs[2], cs[1], cs[0]}
	a, b := Resolve(Input{Policy: base(), Candidates: cs}), Resolve(Input{Policy: base(), Candidates: rev})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("order dependent:\n%+v\n%+v", a, b)
	}
	if cs[0].ID != "b" {
		t.Fatal("input slice reordered")
	}
}

func TestResolutionKeyChangesWithEveryComponent(t *testing.T) {
	k := ResolutionKey(1, "d", 1)
	if k == ResolutionKey(2, "d", 1) || k == ResolutionKey(1, "e", 1) || k == ResolutionKey(1, "d", 2) || k != ResolutionKey(1, "d", 1) {
		t.Fatal("key must depend on each input and be stable")
	}
	if ResolutionKey(1, "ab", 1) == ResolutionKey(1, "a", 1) {
		t.Fatal("collision")
	}
}

func TestScoreEqualToTheThresholdIsNotBelowIt(t *testing.T) {
	p := base() // MinConfidence 0.8, Calibrated 0.9
	out := resolve(p, cand("a", 0, 4, "person", kernel.SourceModel, 0.8))
	if out.Stats.BelowMinConfidence != 0 || len(out.Dispositions) != 1 {
		t.Fatalf("a score equal to the minimum is handled, not filtered: %+v", out)
	}
	if out.Stats.LowConfidence != 1 {
		t.Fatalf("0.8 is below the calibrated 0.9: %+v", out.Stats)
	}
	out = resolve(p, cand("b", 0, 4, "person", kernel.SourceModel, 0.9))
	if out.Stats.LowConfidence != 0 {
		t.Fatalf("a score equal to the calibrated value is not low-confidence: %+v", out.Stats)
	}
	out = resolve(p, cand("c", 0, 4, "person", kernel.SourceModel, 0.79))
	if out.Stats.BelowMinConfidence != 1 || len(out.Dispositions) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestHandMarksOfACategoryWithoutActionArePlaceholdersAnyCategory(t *testing.T) {
	// No category list: whatever a person marks (standard, model-declared or user-defined) is removed and
	// named by its placeholder; a detected candidate of such a category still asks a person.
	out := resolve(base(),
		cand("org", 0, 4, "organization", kernel.SourceManual, 0),
		cand("custom", 10, 14, "cfg_0123456789abcdef01234567", kernel.SourceManual, 0),
		cand("ruled", 20, 24, "cfg_0123456789abcdef01234567", kernel.SourceRule, 0),
		cand("phone", 30, 41, "phone", kernel.SourceManual, 0),
	)
	got := map[string]Action{}
	for _, d := range out.Dispositions {
		got[d.ID] = d.Action
	}
	want := map[string]Action{"org": Placeholder, "custom": Placeholder, "phone": Delete}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dispositions = %v", got)
	}
	if len(out.Drafts) != 1 || out.Drafts[0].Reason != "no_action_for_category" {
		t.Fatalf("drafts = %+v", out.Drafts)
	}
}

func TestADecidedRedactionOfAHandMarkWithoutActionIsAPlaceholderToo(t *testing.T) {
	out := Resolve(Input{Policy: base(), Candidates: []Candidate{
		cand("m", 0, 4, "organization", kernel.SourceManual, 0),
		cand("r", 10, 14, "organization", kernel.SourceRule, 0),
	}, Decisions: map[string]Decision{"m": {}, "r": {}}})
	got := map[string]Action{}
	for _, d := range out.Dispositions {
		got[d.ID] = d.Action
	}
	// a detected candidate a person chose to redact, without an action, is still removed outright
	if got["m"] != Placeholder || got["r"] != Delete {
		t.Fatalf("dispositions = %v", got)
	}
}

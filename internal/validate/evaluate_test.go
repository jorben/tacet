package validate

import (
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/plan"
)

func spec(unit string, s, e int) plan.Spec {
	return plan.Spec{Kind: plan.TextOp, Unit: unit, Start: s, End: e, Action: "placeholder", Category: "phone", Source: []plan.Segment{{Locator: plan.Locator(doc, 1)}}, ContainerEnd: 50}
}

func twoOps(t *testing.T) plan.Plan {
	t.Helper()
	p, err := plan.Build("in", "res", "r", []plan.Spec{spec("u1", 4, 15), spec("u2", 0, 5)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// base is a clean validation of twoOps: every part accounted for, every check executed.
func base(t *testing.T) Inputs {
	p := twoOps(t)
	checks := map[string]Check{}
	for _, o := range p.Operations {
		checks[o.ID] = Check{Executed: true}
	}
	return Inputs{Plan: p,
		Report: Report{Reopened: true, Checks: checks,
			InputParts:  []coverage.InputPart{{ID: doc, ContentType: "t/main"}},
			OutputParts: []coverage.OutputPart{{ID: doc, ContentType: "t/main", SourceID: doc}}},
		Destinations: []coverage.Destination{{PartID: doc, Fate: coverage.FateRedacted}},
		Scan:         extract.ScanResult{Parts: map[string]string{doc: "inspected"}},
		Widen:        func([]plan.Residual) error { return nil }}
}

func TestACleanValidationPasses(t *testing.T) {
	o, err := Evaluate(base(t))
	if err != nil || o.Conclusion != Passed || o.Facts != (Facts{Complete: true, Reopened: true}) {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestEveryWayTheOutputCanBeWrongHasItsConclusion(t *testing.T) {
	op0 := func(in Inputs) string { return in.Plan.Operations[0].ID }
	for _, c := range []struct {
		name string
		edit func(*Inputs)
		want Conclusion
	}{
		{"candidate not reopened", func(in *Inputs) { in.Report.Reopened = false; in.Report.Checks = nil; in.Report.OutputParts = nil }, FailedCorrupt},
		{"part reconciliation mismatch", func(in *Inputs) { in.Report.OutputParts = nil }, FailedCorrupt},
		{"operation neither done nor residual", func(in *Inputs) { in.Report.Checks[op0(*in)] = Check{} }, FailedCorrupt},
		{"check missing for an operation", func(in *Inputs) { delete(in.Report.Checks, op0(*in)) }, FailedCorrupt},
		{"residual, widening possible", func(in *Inputs) { in.Report.Checks[op0(*in)] = Check{Residual: true} }, Widened},
		{"residual, widening used", func(in *Inputs) { in.Report.Checks[op0(*in)] = Check{Residual: true}; in.WidenUsed = true }, FailedResidual},
		{"residual, widening impossible", func(in *Inputs) {
			in.Report.Checks[op0(*in)] = Check{Residual: true}
			in.Widen = func([]plan.Residual) error { return plan.ErrWidenOutOfScope }
		}, FailedResidual},
		{"residual, no widener wired", func(in *Inputs) { in.Report.Checks[op0(*in)] = Check{Residual: true}; in.Widen = nil }, FailedResidual},
		{"a residual flagged on an executed check is still a residual", func(in *Inputs) { in.Report.Checks[op0(*in)] = Check{Executed: true, Residual: true} }, Widened},
		{"new hit in retained text", func(in *Inputs) {
			in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 20, Start: 0, End: 3, Category: "person"}}
		}, NewFindings},
		{"hit inside generated text", func(in *Inputs) {
			in.Edits = []Edit{{Part: doc, Ordinal: 1, Start: 4, End: 15, ReplLen: 5}}
			in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 13, Start: 4, End: 9, Category: "custom"}}
		}, NewFindings},
		{"hit that cannot be located", func(in *Inputs) {
			in.Edits = []Edit{{Part: doc, Ordinal: 1, Start: 0, End: 2, ReplLen: 50}}
			in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 13, Start: 0, End: 3, Category: "x"}}
		}, NewFindings},
		{"text in an output declared to have none", func(in *Inputs) { in.ExpectNoText = true; in.Scan.Units = 1 }, FailedCorrupt},
		{"a hit in an output declared to have no text", func(in *Inputs) {
			in.ExpectNoText = true
			in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 5, Start: 0, End: 3}}
		}, FailedCorrupt},
		{"no text in an output declared to have none", func(in *Inputs) { in.ExpectNoText = true }, Passed},
	} {
		in := base(t)
		c.edit(&in)
		o, err := Evaluate(in)
		if err != nil || o.Conclusion != c.want {
			t.Errorf("%s: %s want %s (%v) facts=%+v", c.name, o.Conclusion, c.want, err, o.Facts)
		}
	}
}

func TestResidualsAreReportedByPlanUnitOnceAndOnlyAsKindAndUnit(t *testing.T) {
	in := base(t)
	var got []plan.Residual
	in.Widen = func(r []plan.Residual) error { got = r; return nil }
	for _, op := range in.Plan.Operations {
		in.Report.Checks[op.ID] = Check{Residual: true}
	}
	in.Plan.Operations = append(in.Plan.Operations, in.Plan.Operations[0]) // the same unit twice
	in.Report.Checks[in.Plan.Operations[0].ID] = Check{Residual: true}
	o, _ := Evaluate(in)
	if o.Conclusion != Widened || len(got) != 2 || got[0] != (plan.Residual{Kind: plan.TextOp, Unit: "u1"}) || got[1].Unit != "u2" {
		t.Fatalf("%+v %v", got, o)
	}
}

func TestWidenIsNotAskedForWhenNothingIsLeftBehindOrItWasUsed(t *testing.T) {
	asked := 0
	in := base(t)
	in.Widen = func([]plan.Residual) error { asked++; return nil }
	Evaluate(in)
	in.WidenUsed = true
	in.Report.Checks[in.Plan.Operations[0].ID] = Check{Residual: true}
	Evaluate(in)
	if asked != 0 {
		t.Fatalf("asked %d times", asked)
	}
}

func TestNewFindingsInsideKnownOnesDoNotCount(t *testing.T) {
	in := base(t)
	in.Edits = []Edit{{Part: doc, Ordinal: 1, Start: 4, End: 15, ReplLen: 5}}
	in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 13, Start: 9, End: 13, Category: "person"}}
	in.Known = []Known{{Part: doc, Ordinal: 1, Start: 15, End: 19}}
	if o, _ := Evaluate(in); o.Conclusion != Passed {
		t.Fatalf("%+v", o)
	}
	in.Known = nil
	o, _ := Evaluate(in)
	if o.Conclusion != NewFindings || len(o.Association.New) != 1 || o.Association.New[0].Start != 15 {
		t.Fatalf("%+v", o)
	}
}

func TestPartsTheRescanDidNotReadInFullAreUnverifiedNotSilentlyPassed(t *testing.T) {
	in := base(t)
	in.Scan.Parts = map[string]string{doc: "inspected", "custom/x.bin": "not_inspected", "word/f.xml": "failed"}
	in.Report.Unverified = []Unverified{{Scope: "a", Reason: "PART_NOT_INSPECTED"}}
	o, _ := Evaluate(in)
	if o.Conclusion != Passed || len(o.Unverified) != 3 || o.Unverified[0].Scope != "a" || o.Unverified[1].Scope != "custom/x.bin" || o.Unverified[1].Reason != "CANDIDATE_PART_NOT_RESCANNED" || o.Unverified[2].Scope != "word/f.xml" {
		t.Fatalf("%+v", o.Unverified)
	}
}

func TestInconsistentInputsAreErrorsNotConclusions(t *testing.T) {
	in := base(t)
	in.Scan.Hits = []extract.Hit{{Part: doc, Ordinal: 1, Chars: 5, Start: 4, End: 9}} // past the unit's own length
	in.Scan.Hits[0].End = 9
	if _, err := Evaluate(in); !errors.Is(err, ErrAssociate) {
		t.Fatalf("%v", err)
	}
	in = base(t)
	in.Destinations = append(in.Destinations, coverage.Destination{PartID: "x", Fate: "kept"})
	if o, err := Evaluate(in); err != nil || o.Conclusion != FailedCorrupt {
		t.Fatalf("a claim for an unknown part must be a mismatch, not an error: %+v %v", o, err)
	}
}

func TestEditsOfDerivesTheReplacedSpansAndLengthsFromThePlan(t *testing.T) {
	p := twoOps(t)
	region, _ := plan.Build("in", "res", "r", []plan.Spec{{Kind: plan.PartOp, Unit: "docProps/core.xml", Action: "remove_part"}})
	if e, err := EditsOf(region, func(plan.Operation) (string, error) { return "", nil }); err != nil || len(e) != 0 {
		t.Fatalf("part operations are not text edits: %v %v", e, err)
	}
	e, err := EditsOf(p, func(o plan.Operation) (string, error) {
		if o.Spec.Unit == "u1" {
			return "[手机号]", nil
		}
		return "", nil
	})
	if err != nil || len(e) != 2 {
		t.Fatalf("%v %v", e, err)
	}
	by := map[int]Edit{}
	for _, x := range e {
		by[x.Start] = x
	}
	if by[4] != (Edit{Part: doc, Ordinal: 1, Start: 4, End: 15, ReplLen: 5}) || by[0].ReplLen != 0 {
		t.Fatalf("%+v", by)
	}
	bad := p
	bad.Operations = append([]plan.Operation(nil), p.Operations...)
	bad.Operations[0].Spec.Source = nil
	if _, err = EditsOf(bad, func(plan.Operation) (string, error) { return "", nil }); !errors.Is(err, plan.ErrNoReliableSource) {
		t.Fatalf("%v", err)
	}
	if _, err = EditsOf(p, func(plan.Operation) (string, error) { return "", errors.New("no") }); err == nil {
		t.Fatal("a failing renderer was ignored")
	}
}

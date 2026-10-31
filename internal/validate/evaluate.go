package validate

import (
	"sort"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/plan"
)

// EditsOf turns the plan's text operations into the geometry Associate needs: which
// input span each one replaced and how long its replacement is. replacement is the
// same function the plan was encoded with, so both sides agree by construction.
func EditsOf(p plan.Plan, replacement func(plan.Operation) (string, error)) ([]Edit, error) {
	var out []Edit
	for _, o := range p.Operations {
		if o.Spec.Kind != plan.TextOp {
			continue
		}
		if len(o.Spec.Source) != 1 {
			return nil, plan.ErrNoReliableSource
		}
		part, ordinal, ok := plan.ParseLocator(o.Spec.Source[0].Locator)
		if !ok {
			return nil, plan.ErrNoReliableSource
		}
		text, err := replacement(o)
		if err != nil {
			return nil, err
		}
		out = append(out, Edit{Part: part, Ordinal: ordinal, Start: o.Spec.Start, End: o.Spec.End, ReplLen: utf8.RuneCountInString(text)})
	}
	return out, nil
}

// Inputs is everything the judgement reads; Evaluate does no I/O.
type Inputs struct {
	Plan         plan.Plan
	Report       Report
	Destinations []coverage.Destination
	Scan         extract.ScanResult
	Edits        []Edit
	Known        []Known
	WidenUsed    bool
	// Widen tries plan.Widen for these residuals and reports whether it worked.
	Widen func([]plan.Residual) error
	// ExpectNoText is set for outputs that have no text layer by design (rasterised
	// PDF): any text found there means the output is not what was declared.
	ExpectNoText bool
}

// Outcome is the judgement with the facts it rests on.
type Outcome struct {
	Conclusion Conclusion
	Facts      Facts
	Mismatches []coverage.Mismatch
	// Residuals are the approved operations whose original content remains;
	// Unaccounted are those neither verified nor left as residue.
	Residuals   []plan.Residual
	Unaccounted []string
	Association Association
	Unverified  []Unverified
}

// Evaluate is module design 8.2 steps 2 to 5 as one deterministic function.
func Evaluate(in Inputs) (Outcome, error) {
	var o Outcome
	f := Facts{Complete: true, Reopened: in.Report.Reopened, WidenUsed: in.WidenUsed}
	o.Unverified = append(o.Unverified, in.Report.Unverified...)

	if f.Reopened {
		mm, err := coverage.Reconcile(in.Report.InputParts, in.Destinations, in.Report.OutputParts, true, true)
		if err != nil {
			return Outcome{}, err
		}
		o.Mismatches, f.Mismatches = mm, len(mm)
		for _, op := range in.Plan.Operations {
			c, ok := in.Report.Checks[op.ID]
			switch {
			case !ok:
				o.Unaccounted = append(o.Unaccounted, op.ID)
			case c.Residual:
				o.Residuals = append(o.Residuals, plan.Residual{Kind: op.Spec.Kind, Unit: op.Spec.Unit})
			case !c.Executed:
				o.Unaccounted = append(o.Unaccounted, op.ID)
			}
		}
		f.Residuals, f.Unaccounted = len(o.Residuals), len(o.Unaccounted)
		if f.Residuals > 0 && !f.WidenUsed && in.Widen != nil {
			f.CanWiden = in.Widen(dedupe(o.Residuals)) == nil
		}
		if in.ExpectNoText && (in.Scan.Units > 0 || len(in.Scan.Hits) > 0) {
			f.Mismatches++ // text in an output declared to have none
		}
		assoc, err := Associate(in.Scan.Hits, in.Edits, in.Known)
		if err != nil {
			return Outcome{}, err
		}
		o.Association = assoc
		f.NewFindings = len(assoc.New) + len(assoc.Generated) + len(assoc.Unlocated)
		for part, state := range in.Scan.Parts {
			if state != "inspected" {
				o.Unverified = append(o.Unverified, Unverified{Scope: part, Reason: "CANDIDATE_PART_NOT_RESCANNED"})
			}
		}
	}
	SortUnverified(o.Unverified)
	o.Facts, o.Conclusion = f, Classify(f)
	return o, nil
}

func dedupe(rs []plan.Residual) []plan.Residual {
	seen := map[plan.Residual]bool{}
	var out []plan.Residual
	for _, r := range rs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Unit < out[j].Unit
	})
	return out
}

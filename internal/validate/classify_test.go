package validate

import "testing"

func TestClassifyTakesTheFirstApplicableConditionInTheTablesOrder(t *testing.T) {
	ok := Facts{Complete: true, Reopened: true}
	with := func(edit func(*Facts)) Facts { f := ok; edit(&f); return f }
	for _, c := range []struct {
		name string
		f    Facts
		want Conclusion
	}{
		{"clean", ok, Passed},
		{"incomplete beats everything", Facts{Reopened: false, Mismatches: 3, Residuals: 2, NewFindings: 9}, NotRun},
		{"not reopened", with(func(f *Facts) { f.Reopened = false }), FailedCorrupt},
		{"part mismatch", with(func(f *Facts) { f.Mismatches = 1 }), FailedCorrupt},
		{"operation neither executed nor left as residual", with(func(f *Facts) { f.Unaccounted = 1 }), FailedCorrupt},
		{"corrupt beats residual", with(func(f *Facts) { f.Mismatches, f.Residuals, f.CanWiden = 1, 1, true }), FailedCorrupt},
		{"residual that can be widened", with(func(f *Facts) { f.Residuals, f.CanWiden = 2, true }), Widened},
		{"residual, widening already used", with(func(f *Facts) { f.Residuals, f.CanWiden, f.WidenUsed = 2, true, true }), FailedResidual},
		{"residual that cannot be widened", with(func(f *Facts) { f.Residuals = 1 }), FailedResidual},
		{"widened beats new findings", with(func(f *Facts) { f.Residuals, f.CanWiden, f.NewFindings = 1, true, 4 }), Widened},
		{"residual beats new findings", with(func(f *Facts) { f.Residuals, f.NewFindings = 1, 4 }), FailedResidual},
		{"new findings alone", with(func(f *Facts) { f.NewFindings = 1 }), NewFindings},
		{"can widen with nothing to widen", with(func(f *Facts) { f.CanWiden = true }), Passed},
	} {
		if got := Classify(c.f); got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
	}
}

func TestStatusIsNeverBetterThanTheConclusionAndAKeptGapMakesItPartial(t *testing.T) {
	for _, c := range []struct {
		c    Conclusion
		kept bool
		want Status
	}{
		{NotRun, false, StatusNotRun}, {NotRun, true, StatusNotRun},
		{FailedCorrupt, false, StatusFailed}, {Widened, false, StatusFailed}, {FailedResidual, true, StatusFailed},
		{NewFindings, false, StatusPassed}, {NewFindings, true, StatusPartial},
		{Passed, false, StatusPassed}, {Passed, true, StatusPartial},
	} {
		if got := StatusOf(c.c, c.kept); got != c.want {
			t.Errorf("%+v -> %s", c, got)
		}
	}
	if StatusOf("something new", false) == StatusPassed {
		t.Error("an unknown conclusion must not read as a pass")
	}
}

func TestUnverifiedItemsAreOrderedStably(t *testing.T) {
	u := []Unverified{{"b", "X"}, {"a", "Z"}, {"a", "Y"}}
	SortUnverified(u)
	if u[0] != (Unverified{"a", "Y"}) || u[1] != (Unverified{"a", "Z"}) || u[2] != (Unverified{"b", "X"}) {
		t.Fatalf("%v", u)
	}
}

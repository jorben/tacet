package exceptions

import (
	"errors"
	"testing"
)

func find(t *testing.T, as []Action, k ActionKind) Action {
	t.Helper()
	for _, a := range as {
		if a.Kind == k {
			return a
		}
	}
	t.Fatalf("action %s not offered in %+v", k, as)
	return Action{}
}

func kinds(as []Action) []ActionKind {
	var out []ActionKind
	for _, a := range as {
		out = append(out, a.Kind)
	}
	return out
}

func TestEveryTypeOffersExactlyItsTableActions(t *testing.T) {
	want := map[Type][]ActionKind{
		PasswordRequired: {Unlock, Skip}, FileFailed: {Reprocess, Skip}, StrictConfirm: {ConfirmExport, Skip},
	}
	for ty, info := range Table {
		as, err := Actions(ty, Context{PartialEligible: true})
		if err != nil {
			t.Fatal(err)
		}
		exp, ok := want[ty]
		if !ok {
			exp = []ActionKind{Redact, Keep}
		}
		got := kinds(as)
		if len(got) != len(exp) || got[0] != exp[0] || got[1] != exp[1] {
			t.Errorf("%s: got %v want %v", ty, got, exp)
		}
		if (info.Level == FileLevel) != (ok) {
			t.Errorf("%s: level and action family disagree", ty)
		}
	}
}

func TestKeepOnUncheckedContentNeedsPartialEligibility(t *testing.T) {
	for _, ty := range []Type{ScanPage, ImageWithText, EmbeddedObject, UninspectedPart} {
		as, _ := Actions(ty, Context{PartialEligible: true})
		k := find(t, as, Keep)
		if !k.Available || k.Consequence != PartialResult || !k.BulkNeedsConfirm {
			t.Errorf("%s eligible: %+v", ty, k)
		}
		as, _ = Actions(ty, Context{})
		k = find(t, as, Keep)
		if k.Available || k.Reason != ReasonPartialNotEligible {
			t.Errorf("%s ineligible keep must be disabled with a reason: %+v", ty, k)
		}
		if !find(t, as, Redact).Available {
			t.Errorf("%s: redact must stay available", ty)
		}
	}
}

func TestKeepConsequencesOfOtherContentTypes(t *testing.T) {
	as, _ := Actions(OutlinedText, Context{})
	if k := find(t, as, Keep); !k.Available || k.Consequence != ManualConfirmation {
		t.Fatalf("outlined text keep: %+v", k)
	}
	for _, ty := range []Type{ValidatorMismatch, TypeChange, PolicyReview, UnreliableLocation} {
		as, _ := Actions(ty, Context{})
		if k := find(t, as, Keep); !k.Available || k.Consequence != NoConsequence {
			t.Errorf("%s keep: %+v", ty, k)
		}
	}
	as, _ = Actions(UnreliableLocation, Context{})
	if !find(t, as, Redact).NeedsRelocation {
		t.Fatal("unreliable location redaction needs relocation or a larger authorized range")
	}
	as, _ = Actions(ScanPage, Context{PartialEligible: true})
	if find(t, as, Redact).NeedsRelocation {
		t.Fatal("relocation only applies to unreliable locations")
	}
}

func TestReprocessIsOfferedOnceAndSkipCountsAsUndelivered(t *testing.T) {
	as, _ := Actions(FileFailed, Context{})
	if !find(t, as, Reprocess).Available {
		t.Fatal("first reprocess must be available")
	}
	as, _ = Actions(FileFailed, Context{ReprocessUsed: true})
	if r := find(t, as, Reprocess); r.Available || r.Reason != ReasonReprocessUsed {
		t.Fatalf("spent reprocess: %+v", r)
	}
	for _, ty := range []Type{PasswordRequired, FileFailed, StrictConfirm} {
		as, _ := Actions(ty, Context{})
		if s := find(t, as, Skip); !s.Available || s.Consequence != Undelivered || !s.BulkNeedsConfirm {
			t.Errorf("%s skip: %+v", ty, s)
		}
	}
}

func TestGroupDecisionRules(t *testing.T) {
	cases := []struct {
		t      Type
		c      Context
		expect bool
	}{
		{PasswordRequired, Context{}, true}, {FileFailed, Context{}, true}, {ScanPage, Context{}, true},
		{StrictConfirm, Context{}, false}, {UnreliableLocation, Context{PolicyAllowsGroup: true}, false},
		{PolicyReview, Context{}, false}, {PolicyReview, Context{PolicyAllowsGroup: true}, true},
	}
	for _, c := range cases {
		got, err := CanDecideGroup(c.t, c.c)
		if err != nil || got != c.expect {
			t.Errorf("%s %+v: got %v err %v want %v", c.t, c.c, got, err, c.expect)
		}
	}
}

func TestUnknownTypesAreErrorsNotDefaults(t *testing.T) {
	if _, err := Actions("made_up", Context{}); !errors.Is(err, ErrUnknownType) {
		t.Fatal("unknown type offered actions")
	}
	if _, err := CanDecideGroup("made_up", Context{}); !errors.Is(err, ErrUnknownType) {
		t.Fatal("unknown type groupable")
	}
}

func TestPluginReportsAreRestrictedToContentCodes(t *testing.T) {
	for ty, info := range Table {
		got, err := ValidatePluginReport(string(ty))
		if info.PluginReportable {
			if err != nil || got != ty {
				t.Errorf("%s should be accepted: %v", ty, err)
			}
		} else if !errors.Is(err, ErrNotPluginReported) {
			t.Errorf("%s must be kernel-only: %v", ty, err)
		}
	}
	for _, code := range []string{"", "password_required", "SCAN_PAGE", "scan_page "} {
		if _, err := ValidatePluginReport(code); err == nil {
			t.Errorf("%q accepted", code)
		}
	}
	if _, err := ValidatePluginReport("custom_action"); !errors.Is(err, ErrUnknownType) {
		t.Fatal("plugin-defined type accepted")
	}
}

func TestItemIDIsStableAndDistinct(t *testing.T) {
	a := ItemID(ScanPage, "part:p3")
	if a != ItemID(ScanPage, "part:p3") {
		t.Fatal("id not stable")
	}
	if a == ItemID(ScanPage, "part:p4") || a == ItemID(ImageWithText, "part:p3") {
		t.Fatal("distinct items share an id")
	}
	if ItemID("ab", "c") == ItemID("a", "bc") {
		t.Fatal("type/scope boundary ambiguity")
	}
}

func TestAggregateGroupsByCauseNotByItem(t *testing.T) {
	items := []Item{
		{ScanPage, "no_ocr", "f1", false}, {ScanPage, "no_ocr", "f1", false}, {ScanPage, "no_ocr", "f2", false},
		{ScanPage, "no_ocr", "f3", true}, // resolved
		{ImageWithText, "stamp", "f1", false},
		{ScanPage, "low_quality", "f2", false},
	}
	got := Aggregate(items)
	want := []Group{
		{ImageWithText, "stamp", 1, 1},
		{ScanPage, "low_quality", 1, 1},
		{ScanPage, "no_ocr", 3, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: %+v want %+v", i, got[i], want[i])
		}
	}
	if len(Aggregate(nil)) != 0 {
		t.Fatal("empty input")
	}
}

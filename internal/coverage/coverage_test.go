package coverage

import (
	"errors"
	"reflect"
	"testing"
)

func part(id string, class ContentClass, req, known, bounded bool, ins Inspection, disp Disposition) Part {
	return Part{ID: id, Class: class, Required: req, Known: known, Bounded: bounded, Inspection: ins, Disposition: disp}
}

func TestIsGapRules(t *testing.T) {
	cases := []struct {
		name     string
		p        Part
		gap, res bool
	}{
		{"inspected text", part("a", ClassText, true, true, true, Inspected, RetainedByPolicy), false, false},
		{"required, not inspected, pending", part("a", ClassImage, true, true, true, NotInspected, Pending), true, false},
		{"required, unsupported, kept", part("a", ClassImage, true, true, true, Unsupported, RetainedByPolicy), true, false},
		{"required, failed inspection", part("a", ClassText, true, true, true, InspectFail, Pending), true, false},
		{"required, not inspected, removed", part("a", ClassImage, true, true, true, NotInspected, Removed), true, true},
		{"required, not inspected, redacted", part("a", ClassImage, true, true, true, NotInspected, Redacted), true, true},
		{"unknown content even if inspected", part("a", ClassOther, false, false, false, Inspected, RetainedByPolicy), true, false},
		{"unknown content removed", part("a", ClassOther, false, false, false, NotInspected, Removed), true, true},
		{"known, out of scope, not inspected", part("a", ClassOther, false, true, true, NotInspected, RetainedByPolicy), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gap, res := IsGap(c.p)
			if gap != c.gap || res != c.res {
				t.Fatalf("got gap=%v resolved=%v", gap, res)
			}
		})
	}
}

func TestAssessSeparatesUnresolvedRetainedResolvedAndOutOfScope(t *testing.T) {
	parts := []Part{
		part("text", ClassText, true, true, true, Inspected, Redacted),
		part("scan", ClassScanPage, true, true, true, NotInspected, Pending),
		part("img", ClassImage, true, true, true, NotInspected, RetainedByPolicy),
		part("att", ClassEmbedded, true, true, true, Unsupported, Removed),
		part("outline", ClassOutlined, true, true, true, NotInspected, Pending),
		part("mystery", ClassOther, false, false, false, NotInspected, Pending),
		part("thumb", ClassOther, false, true, true, NotInspected, RetainedByPolicy),
	}
	r, err := Assess(parts, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]GapKind{}
	for _, g := range r.Unresolved {
		got[g.PartID] = g.Kind
	}
	want := map[string]GapKind{"scan": GapScanPage, "outline": GapOutlinedText, "mystery": GapUninspectedPart}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved %v", got)
	}
	if len(r.Retained) != 1 || r.Retained[0].PartID != "img" || r.Retained[0].Kind != GapImageWithText {
		t.Fatalf("retained %+v", r.Retained)
	}
	if r.Resolved != 1 || r.OutOfScope != 1 {
		t.Fatalf("resolved=%d outOfScope=%d", r.Resolved, r.OutOfScope)
	}
}

func TestIncompleteInventoryIsAnErrorNotAGapList(t *testing.T) {
	if _, err := Assess(nil, false); !errors.Is(err, ErrInventoryIncomplete) {
		t.Fatal("incomplete inventory must fail the stage")
	}
	if PartialBeforeExport(nil, false) {
		t.Fatal("incomplete inventory cannot be partial-eligible")
	}
}

func TestPartialEligibilityNeedsBothSteps(t *testing.T) {
	bounded := []Part{part("img", ClassImage, true, true, true, NotInspected, RetainedByPolicy)}
	opaque := append([]Part{}, bounded...)
	opaque = append(opaque, part("mystery", ClassOther, false, false, false, NotInspected, RetainedByPolicy))
	removedOpaque := append([]Part{}, bounded...)
	removedOpaque = append(removedOpaque, part("mystery", ClassOther, false, false, false, NotInspected, Removed))
	if !PartialBeforeExport(bounded, true) || PartialBeforeExport(opaque, true) || !PartialBeforeExport(removedOpaque, true) {
		t.Fatal("step one: only bounded kept gaps may remain")
	}
	ok := PostValidation{true, true, true}
	if !PartialEligible(true, ok) {
		t.Fatal("both steps hold")
	}
	for _, v := range []PostValidation{{false, true, true}, {true, false, true}, {true, true, false}} {
		if PartialEligible(true, v) {
			t.Fatalf("post-validation %+v must block", v)
		}
	}
	if PartialEligible(false, ok) {
		t.Fatal("step one failed")
	}
}

func recon(t *testing.T, in []InputPart, d []Destination, out []OutputPart) []Mismatch {
	t.Helper()
	m, err := Reconcile(in, d, out, true, true)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func baseline() ([]InputPart, []Destination, []OutputPart) {
	return []InputPart{{"doc", "xml"}, {"media", "png"}, {"thumb", "jpeg"}},
		[]Destination{{PartID: "doc", Fate: FateRedacted}, {PartID: "media", Fate: FateConverted, ExpectedType: "png-placeholder"}, {PartID: "thumb", Fate: FateRemoved}},
		[]OutputPart{{ID: "o1", ContentType: "xml", SourceID: "doc", References: []string{"o2"}}, {ID: "o2", ContentType: "png-placeholder", SourceID: "media"}, {ID: "new1", ContentType: "xml"}}
}

func TestReconcileAcceptsConsistentWrite(t *testing.T) {
	in, d, out := baseline()
	if m := recon(t, in, d, out); len(m) != 0 {
		t.Fatalf("clean write mismatched: %+v", m)
	}
}

func TestReconcileDetectsEveryMismatchKind(t *testing.T) {
	cases := []struct {
		name   string
		change func(*[]InputPart, *[]Destination, *[]OutputPart)
		want   Mismatch
	}{
		{"input without destination", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { *d = (*d)[:2] }, Mismatch{MissingDestination, "thumb"}},
		{"destination for unknown input", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) {
			*d = append(*d, Destination{PartID: "ghost", Fate: FateKept})
		}, Mismatch{UnknownSource, "ghost"}},
		{"duplicate destination", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) {
			*d = append(*d, Destination{PartID: "doc", Fate: FateKept})
		}, Mismatch{DuplicateDestination, "doc"}},
		{"output claims an unregistered source", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) {
			*o = append(*o, OutputPart{ID: "o9", ContentType: "x", SourceID: "ghost"})
		}, Mismatch{UnregisteredOutput, "o9"}},
		{"removed part still present", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) {
			*o = append(*o, OutputPart{ID: "o3", ContentType: "jpeg", SourceID: "thumb"})
		}, Mismatch{RemovedStillPresent, "thumb"}},
		{"reference to a removed part's copy", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) {
			*o = append(*o, OutputPart{ID: "o3", ContentType: "jpeg", SourceID: "thumb"})
			(*o)[0].References = append((*o)[0].References, "o3")
		}, Mismatch{DanglingReference, "o1"}},
		{"kept part missing", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { *o = (*o)[1:] }, Mismatch{KeptMissing, "doc"}},
		{"content type mismatch", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { (*o)[0].ContentType = "html" }, Mismatch{ContentTypeMismatch, "o1"}},
		{"converted to an unexpected type", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { (*o)[1].ContentType = "png" }, Mismatch{ContentTypeMismatch, "o2"}},
		{"duplicate output id", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { *o = append(*o, (*o)[2]) }, Mismatch{DuplicateOutput, "new1"}},
		{"unknown fate", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { (*d)[0].Fate = "vanished" }, Mismatch{InvalidFate, "doc"}},
		{"conversion without expected type", func(i *[]InputPart, d *[]Destination, o *[]OutputPart) { (*d)[1].ExpectedType = "" }, Mismatch{InvalidFate, "media"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, d, out := baseline()
			c.change(&in, &d, &out)
			found := false
			for _, m := range recon(t, in, d, out) {
				found = found || m == c.want
			}
			if !found {
				t.Fatalf("missing %+v in %+v", c.want, recon(t, in, d, out))
			}
		})
	}
}

func TestReconcileRefusesIncompleteEnumerationAndIsDeterministic(t *testing.T) {
	in, d, out := baseline()
	for _, flags := range [][2]bool{{false, true}, {true, false}} {
		if _, err := Reconcile(in, d, out, flags[0], flags[1]); !errors.Is(err, ErrInventoryIncomplete) {
			t.Fatalf("flags %v accepted", flags)
		}
	}
	out = append(out, OutputPart{ID: "zz", ContentType: "x", SourceID: "ghost"}, OutputPart{ID: "aa", ContentType: "x", SourceID: "ghost2"})
	a, b := recon(t, in, d, out), recon(t, in, d, out)
	if !reflect.DeepEqual(a, b) || len(a) != 2 || a[0].PartID != "zz" && a[0].PartID != "aa" {
		t.Fatalf("unstable order: %+v %+v", a, b)
	}
}

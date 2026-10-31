package validate

import (
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/kernel"
)

func hit(part string, ord, chars, s, e int, cat string) extract.Hit {
	return extract.Hit{Part: part, Ordinal: ord, Chars: chars, Start: s, End: e, Category: cat, Source: kernel.SourceRule, Score: 0.9}
}

const doc = "word/document.xml"

// Input "AAAA13812345678BBBB" (19) with 13812345678 (4..15) replaced by "[手机号]" (5):
// the candidate is "AAAA[手机号]BBBB" (13). Candidate 0-4 and 9-13 are input 0-4 and 15-19.
var oneEdit = []Edit{{Part: doc, Ordinal: 1, Start: 4, End: 15, ReplLen: 5}}

func TestAHitInRetainedTextIsMappedBackToItsInputPosition(t *testing.T) {
	a, err := Associate([]extract.Hit{hit(doc, 1, 13, 10, 12, "person"), hit(doc, 1, 13, 0, 3, "person")}, oneEdit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.New) != 2 || len(a.Generated) != 0 {
		t.Fatalf("%+v", a)
	}
	if a.New[0].Start != 0 || a.New[0].End != 3 {
		t.Fatalf("before the edit nothing moves: %+v", a.New[0])
	}
	if a.New[1].Start != 16 || a.New[1].End != 18 {
		t.Fatalf("after the edit the input coordinates are shifted back by the replacement difference: %+v", a.New[1])
	}
}

func TestAHitInGeneratedTextIsNotNewInputContent(t *testing.T) {
	a, err := Associate([]extract.Hit{hit(doc, 1, 13, 4, 9, "custom")}, oneEdit, nil)
	if err != nil || len(a.New) != 0 || len(a.Generated) != 1 || a.Generated[0] != (Generated{doc, 1, 4, 9}) {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestAHitAcrossTheBoundaryIsSplitAndEachPieceHandledByItsOwnCase(t *testing.T) {
	a, err := Associate([]extract.Hit{hit(doc, 1, 13, 2, 11, "address")}, oneEdit, nil)
	if err != nil {
		t.Fatal(err)
	}
	// candidate 2-4 retained (input 2-4), 4-9 generated, 9-11 retained (input 15-17)
	if len(a.New) != 2 || a.New[0].Start != 2 || a.New[0].End != 4 || a.New[1].Start != 15 || a.New[1].End != 17 {
		t.Fatalf("%+v", a.New)
	}
	if len(a.Generated) != 1 || a.Generated[0].Start != 4 || a.Generated[0].End != 9 {
		t.Fatalf("%+v", a.Generated)
	}
}

func TestADeletionLeavesNoGeneratedTextAndAdjacentRetainedPiecesStaySeparate(t *testing.T) {
	// input "AB1234CD" with 1234 deleted → "ABCD"
	del := []Edit{{Part: doc, Ordinal: 2, Start: 2, End: 6, ReplLen: 0}}
	a, err := Associate([]extract.Hit{hit(doc, 2, 4, 1, 3, "x")}, del, nil)
	if err != nil || len(a.Generated) != 0 || len(a.New) != 2 {
		t.Fatalf("%+v %v", a, err)
	}
	if a.New[0].Start != 1 || a.New[0].End != 2 || a.New[1].Start != 6 || a.New[1].End != 7 {
		t.Fatalf("%+v", a.New)
	}
}

func TestSeveralEditsInOneUnitAndUnitsWithoutEdits(t *testing.T) {
	edits := []Edit{{Part: doc, Ordinal: 1, Start: 10, End: 12, ReplLen: 1}, {Part: doc, Ordinal: 1, Start: 2, End: 4, ReplLen: 3}} // given out of order
	// input 20 chars → candidate 20 -2+3 -2+1 = 20
	a, err := Associate([]extract.Hit{hit(doc, 1, 20, 18, 20, "p"), hit(doc, 3, 8, 1, 4, "q")}, edits, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first, other *NewFinding
	for i := range a.New {
		if a.New[i].Ordinal == 1 {
			first = &a.New[i]
		} else {
			other = &a.New[i]
		}
	}
	if first == nil || first.Start != 18 || first.End != 20 || other == nil || other.Start != 1 || other.End != 4 {
		t.Fatalf("%+v", a.New)
	}
}

func TestAHitInsideAnInputFindingIsNotNewWhetherItWasRedactedOrKept(t *testing.T) {
	known := []Known{{Part: doc, Ordinal: 1, Start: 15, End: 19}}
	a, _ := Associate([]extract.Hit{hit(doc, 1, 13, 9, 13, "person"), hit(doc, 1, 13, 10, 12, "person")}, oneEdit, known)
	if len(a.New) != 0 {
		t.Fatalf("a hit inside a known finding is not new: %+v", a.New)
	}
	a, _ = Associate([]extract.Hit{hit(doc, 1, 13, 9, 13, "person")}, oneEdit, []Known{{Part: doc, Ordinal: 1, Start: 16, End: 19}})
	if len(a.New) != 1 {
		t.Fatalf("a hit that only partly lies inside a known finding is still new: %+v", a.New)
	}
	a, _ = Associate([]extract.Hit{hit(doc, 1, 13, 9, 13, "person")}, oneEdit, []Known{{Part: doc, Ordinal: 2, Start: 0, End: 99}, {Part: "other.xml", Ordinal: 1, Start: 0, End: 99}})
	if len(a.New) != 1 {
		t.Fatal("findings of another unit must not hide a hit")
	}
}

func TestInconsistentGeometryIsReportedNotGuessed(t *testing.T) {
	// the candidate is shorter than the edits require, or the edits overlap
	for name, edits := range map[string][]Edit{
		"candidate too short": {{Part: doc, Ordinal: 1, Start: 0, End: 2, ReplLen: 50}},
		"overlap":             {{Part: doc, Ordinal: 1, Start: 0, End: 5, ReplLen: 1}, {Part: doc, Ordinal: 1, Start: 3, End: 8, ReplLen: 1}},
		"empty range":         {{Part: doc, Ordinal: 1, Start: 3, End: 3, ReplLen: 1}},
		"negative length":     {{Part: doc, Ordinal: 1, Start: 0, End: 3, ReplLen: -1}},
	} {
		a, err := Associate([]extract.Hit{hit(doc, 1, 13, 0, 3, "p")}, edits, nil)
		if err != nil || len(a.Unlocated) != 1 || len(a.New) != 0 {
			t.Errorf("%s: %+v %v", name, a, err)
		}
	}
	for name, h := range map[string]extract.Hit{"empty": hit(doc, 1, 13, 4, 4, "p"), "reversed": hit(doc, 1, 13, 5, 4, "p"), "past the unit": hit(doc, 1, 13, 10, 14, "p"), "negative": hit(doc, 1, 13, -1, 3, "p")} {
		if _, err := Associate([]extract.Hit{h}, oneEdit, nil); !errors.Is(err, ErrAssociate) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAssociationIsSortedAndIndependentOfHitOrder(t *testing.T) {
	hs := []extract.Hit{hit("b.xml", 1, 10, 0, 2, "p"), hit(doc, 2, 10, 5, 7, "p"), hit(doc, 1, 10, 6, 8, "p"), hit(doc, 1, 10, 1, 3, "p")}
	a1, _ := Associate(hs, nil, nil)
	rev := []extract.Hit{hs[3], hs[2], hs[1], hs[0]}
	a2, _ := Associate(rev, nil, nil)
	if len(a1.New) != 4 {
		t.Fatalf("%+v", a1)
	}
	for i := range a1.New {
		if a1.New[i] != a2.New[i] {
			t.Fatalf("order dependent: %+v %+v", a1.New, a2.New)
		}
	}
	if a1.New[0].Part != "b.xml" || a1.New[1].Start != 1 || a1.New[2].Start != 6 || a1.New[3].Ordinal != 2 {
		t.Fatalf("%+v", a1.New)
	}
}

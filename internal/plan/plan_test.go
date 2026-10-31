package plan

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

var pg = Page{W: 200, H: 100}

func TestRegionsRotationHandAnchoredAt72DPI(t *testing.T) {
	box := []Box{{L: 10, B: 60, R: 30, T: 80}} // pixels (10..30, 20..40) unrotated
	cases := map[int]Rect{
		0:   {10, 20, 30, 40},
		90:  {60, 10, 80, 30},
		180: {170, 60, 190, 80},
		270: {20, 170, 40, 190},
	}
	for rot, want := range cases {
		got, err := Regions(box, Page{W: 200, H: 100, Rotation: rot}, Params{DPI: 72})
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("rotation %d: got %v err %v want %v", rot, got, err, want)
		}
	}
}

func TestRegionsScaleMarginAntialiasAndClipping(t *testing.T) {
	box := []Box{{L: 10, B: 60, R: 30, T: 80}}
	got, _ := Regions(box, pg, Params{DPI: 144, MarginPoints: 1, AntialiasPixels: 1})
	// points 9..31 x 19..41 -> pixels 18..62 x 38..82, plus one pixel each side
	if want := (Rect{17, 37, 63, 83}); len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want %v", got, want)
	}
	// Expansion beyond the page is clipped, never negative or past the edge.
	edge := []Box{{L: 0, B: 0, R: 200, T: 100}}
	got, _ = Regions(edge, pg, Params{DPI: 72, MarginPoints: 5, AntialiasPixels: 2})
	if want := (Rect{0, 0, 200, 100}); len(got) != 1 || got[0] != want {
		t.Fatalf("clip: %v", got)
	}
	// Fractional edges round outward: the region never shrinks.
	frac, _ := Regions([]Box{{L: 10.7, B: 59.8, R: 29.3, T: 80.1}}, pg, Params{DPI: 72})
	if want := (Rect{10, 19, 30, 41}); frac[0] != want {
		t.Fatalf("outward rounding: %v", frac)
	}
	// A box fully outside the page yields no region.
	if out, _ := Regions([]Box{{L: 500, B: 10, R: 510, T: 20}}, pg, Params{DPI: 72}); len(out) != 0 {
		t.Fatalf("outside: %v", out)
	}
}

func TestRegionsMergeCharactersPerLineButKeepColumnsApart(t *testing.T) {
	line := []Box{{10, 60, 18, 72}, {18, 60, 26, 72}, {26, 60.5, 34, 72.5}, {40, 60, 48, 72}} // one line, a space gap of 6
	got, _ := Regions(line, pg, Params{DPI: 72})
	if len(got) != 1 {
		t.Fatalf("one line must be one region: %v", got)
	}
	two := []Box{{10, 60, 18, 72}, {120, 60, 128, 72}} // far apart on the same baseline
	if got, _ = Regions(two, pg, Params{DPI: 72}); len(got) != 2 {
		t.Fatalf("columns must stay separate: %v", got)
	}
	lines := []Box{{10, 60, 18, 72}, {10, 40, 18, 52}}
	got, _ = Regions(lines, pg, Params{DPI: 72})
	if len(got) != 2 || got[0].T > got[1].T {
		t.Fatalf("two lines, top first: %v", got)
	}
	a, _ := Regions(line, pg, Params{DPI: 72})
	b, _ := Regions([]Box{line[3], line[2], line[1], line[0]}, pg, Params{DPI: 72})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("result depends on input order")
	}
}

func TestRegionsRejectInvalidInput(t *testing.T) {
	ok := []Box{{10, 10, 20, 20}}
	bad := []struct {
		name string
		b    []Box
		p    Page
		q    Params
	}{
		{"zero dpi", ok, pg, Params{}},
		{"negative margin", ok, pg, Params{DPI: 72, MarginPoints: -1}},
		{"rotation 45", ok, Page{W: 200, H: 100, Rotation: 45}, Params{DPI: 72}},
		{"empty page", ok, Page{}, Params{DPI: 72}},
		{"nan box", []Box{{math.NaN(), 0, 1, 1}}, pg, Params{DPI: 72}},
		{"inverted box", []Box{{10, 20, 20, 10}}, pg, Params{DPI: 72}},
		{"inf dpi", ok, pg, Params{DPI: math.Inf(1)}},
		{"huge antialias", ok, pg, Params{DPI: 72, AntialiasPixels: 99}},
	}
	for _, c := range bad {
		if _, err := Regions(c.b, c.p, c.q); !errors.Is(err, ErrGeometry) {
			t.Errorf("%s accepted", c.name)
		}
	}
}

func txt(unit string, start, end int, action string) Spec {
	return Spec{Kind: TextOp, Unit: unit, Start: start, End: end, Action: action, Source: []Segment{{Locator: "n:" + unit}}, ContainerEnd: 100}
}

func TestBuildIsDeterministicStableAndOrderIndependent(t *testing.T) {
	specs := []Spec{txt("u2", 5, 9, "delete"), txt("u1", 0, 3, "placeholder"), txt("u1", 10, 12, "mask")}
	a, err := Build("in1", "res1", "r1", specs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build("in1", "res1", "r1", []Spec{specs[2], specs[0], specs[1]})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("plan depends on input order")
	}
	if a.Operations[0].Spec.Unit != "u1" || a.Operations[0].Spec.Start != 0 {
		t.Fatalf("not sorted: %+v", a.Operations)
	}
	// An unchanged operation keeps its id when another one is added.
	more, _ := Build("in1", "res1", "r1", append(specs, txt("u3", 0, 2, "delete")))
	ids := map[string]bool{}
	for _, o := range more.Operations {
		ids[o.ID] = true
	}
	for _, o := range a.Operations {
		if !ids[o.ID] {
			t.Fatal("stable id changed")
		}
	}
	for name, v := range map[string]string{"input": mustVersion(t, "in2", "res1", "r1", specs), "resolution": mustVersion(t, "in1", "res2", "r1", specs), "render": mustVersion(t, "in1", "res1", "r2", specs)} {
		if v == a.Version {
			t.Errorf("%s change did not change the plan version", name)
		}
	}
	if mustVersion(t, "in1", "res1", "r1", specs[:2]) == a.Version {
		t.Error("operation set change did not change the version")
	}
}

func mustVersion(t *testing.T, in, res, render string, specs []Spec) string {
	t.Helper()
	p, err := Build(in, res, render, specs)
	if err != nil {
		t.Fatal(err)
	}
	return p.Version
}

func TestBuildRefusesUnreliableOrInvalidOperations(t *testing.T) {
	noSource := txt("u1", 0, 3, "delete")
	noSource.Source = nil
	emptyLoc := txt("u1", 0, 3, "delete")
	emptyLoc.Source = []Segment{{}}
	region := Spec{Kind: RegionOp, Unit: "page:1", Action: "fill", Rect: &Rect{0, 0, 10, 10}}
	cases := []struct {
		name  string
		specs []Spec
		want  error
	}{
		{"text without source", []Spec{noSource}, ErrNoReliableSource},
		{"empty locator", []Spec{emptyLoc}, ErrNoReliableSource},
		{"empty range", []Spec{txt("u1", 4, 4, "delete")}, ErrInvalidOperation},
		{"negative start", []Spec{txt("u1", -1, 4, "delete")}, ErrInvalidOperation},
		{"no action", []Spec{txt("u1", 0, 4, "")}, ErrInvalidOperation},
		{"unknown kind", []Spec{{Kind: "magic", Unit: "u", Action: "x"}}, ErrInvalidOperation},
		{"region without rect", []Spec{{Kind: RegionOp, Unit: "p", Action: "fill"}}, ErrInvalidOperation},
		{"inverted rect", []Spec{{Kind: RegionOp, Unit: "p", Action: "fill", Rect: &Rect{5, 5, 1, 1}}}, ErrInvalidOperation},
		{"overlap", []Spec{txt("u1", 0, 5, "delete"), txt("u1", 4, 8, "mask")}, ErrOverlap},
		{"duplicate", []Spec{txt("u1", 0, 5, "delete"), txt("u1", 0, 5, "delete")}, ErrOverlap},
	}
	for _, c := range cases {
		if _, err := Build("in", "res", "r", c.specs); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// Touching ranges, regions on the same page and separate units are fine.
	if _, err := Build("in", "res", "r", []Spec{txt("u1", 0, 5, "delete"), txt("u1", 5, 8, "mask"), txt("u2", 0, 5, "delete"), region, region}); !errors.Is(err, ErrOverlap) {
		t.Error("identical regions are duplicates")
	}
	if _, err := Build("in", "res", "r", []Spec{txt("u1", 0, 5, "delete"), txt("u1", 5, 8, "mask"), txt("u2", 0, 5, "delete"), region}); err != nil {
		t.Errorf("valid plan rejected: %v", err)
	}
}

func TestWidenGrowsToTheContainerOnceAndCountsThePatch(t *testing.T) {
	base, _ := Build("in", "res", "r", []Spec{txt("u1", 10, 14, "mask"), txt("u1", 30, 33, "delete"), txt("u2", 0, 2, "delete")})
	w, err := Widen(base, "in", "res", "r", []Residual{{TextOp, "u1"}})
	if err != nil {
		t.Fatal(err)
	}
	if w.Patches != 1 || w.Version == base.Version || len(w.Operations) != 2 {
		t.Fatalf("%+v", w)
	}
	var u1 Operation
	for _, o := range w.Operations {
		if o.Spec.Unit == "u1" {
			u1 = o
		}
	}
	if !u1.Widened || u1.Spec.Start != 0 || u1.Spec.End != 100 || u1.Spec.Action != "delete" || len(u1.Spec.Source) != 2 {
		t.Fatalf("widened op: %+v", u1)
	}
	// The untouched unit keeps its id; the input plan is unchanged.
	if len(base.Operations) != 3 || base.Patches != 0 || base.Operations[0].Widened {
		t.Fatal("input plan mutated")
	}
	if overlapErr := checkOverlap(w.Operations); overlapErr != nil {
		t.Fatalf("widened plan overlaps: %v", overlapErr)
	}
}

func TestWidenRegionAndOutOfScope(t *testing.T) {
	region := Spec{Kind: RegionOp, Unit: "page:1:img3", Action: "fill", Rect: &Rect{10, 10, 20, 20}, ContainerRect: &Rect{0, 0, 100, 80}}
	base, _ := Build("in", "res", "r", []Spec{region, txt("u1", 0, 3, "delete")})
	w, err := Widen(base, "in", "res", "r", []Residual{{RegionOp, "page:1:img3"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range w.Operations {
		if o.Spec.Kind == RegionOp && (*o.Spec.Rect != Rect{0, 0, 100, 80} || !o.Widened) {
			t.Fatalf("region not widened: %+v", o)
		}
	}
	// A base region's rectangle must not have been changed through the shared pointer.
	if *region.Rect != (Rect{10, 10, 20, 20}) || *base.Operations[0].Spec.Rect != (Rect{10, 10, 20, 20}) {
		t.Fatal("input rectangle mutated")
	}
	for name, res := range map[string][]Residual{
		"unit without operation": {{TextOp, "other"}}, "wrong kind": {{RegionOp, "u1"}}, "none": nil,
		"mixed": {{TextOp, "u1"}, {TextOp, "ghost"}},
	} {
		if _, err := Widen(base, "in", "res", "r", res); !errors.Is(err, ErrWidenOutOfScope) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// No known container: refuse rather than guess an extent.
	noContainer := txt("u1", 0, 3, "delete")
	noContainer.ContainerEnd = 0
	p, _ := Build("in", "res", "r", []Spec{noContainer})
	if _, err := Widen(p, "in", "res", "r", []Residual{{TextOp, "u1"}}); !errors.Is(err, ErrWidenOutOfScope) {
		t.Error("unknown container accepted")
	}
	bare := Spec{Kind: RegionOp, Unit: "page:2:img1", Action: "fill", Rect: &Rect{1, 1, 2, 2}}
	rp, _ := Build("in", "res", "r", []Spec{bare})
	if _, err := Widen(rp, "in", "res", "r", []Residual{{RegionOp, "page:2:img1"}}); !errors.Is(err, ErrWidenOutOfScope) {
		t.Error("a region without a known container was widened")
	}
	part, _ := Build("in", "res", "r", []Spec{{Kind: PartOp, Unit: "media/1", Action: "remove_part"}})
	if _, err := Widen(part, "in", "res", "r", []Residual{{PartOp, "media/1"}}); !errors.Is(err, ErrWidenOutOfScope) {
		t.Error("a whole part cannot be widened")
	}
}

func TestAFileIsWidenedAtMostOnce(t *testing.T) {
	base, _ := Build("in", "res", "r", []Spec{txt("u1", 10, 14, "mask"), txt("u2", 0, 2, "delete")})
	w, err := Widen(base, "in", "res", "r", []Residual{{TextOp, "u1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Widen(w, "in", "res", "r", []Residual{{TextOp, "u2"}}); !errors.Is(err, ErrWidenOutOfScope) {
		t.Fatalf("a second widening of the same file: %v", err)
	}
}

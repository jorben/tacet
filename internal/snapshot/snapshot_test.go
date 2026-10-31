package snapshot

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
)

func content() Content {
	return Content{
		Detect: DetectContent{ModelID: "astrlink", ModelDigest: "m1", Backend: "cpu", LabelMap: map[string]string{"phone": "phone", "private_person": "person"},
			AdapterFloor: 0.5, Calibrated: 0.9, RuleSetDigest: "rs1", RulePacks: map[string]string{"p": "v1"}, Categories: []string{"person", "phone"}},
		Dispose:  DisposeContent{CategoryAction: map[string]string{"person": "placeholder", "phone": "delete"}, MinConfidence: 0.8, ImageDefault: "remove", Mode: "guided"},
		Render:   RenderContent{Style: "black", Placeholder: "type", DPI: 200, Output: "raster_pdf"},
		Runtime:  RuntimeContent{Protocol: "1.0", Formats: map[string]PluginRef{"docx": {ID: "tacet.docx", Version: "1", Digest: "d1", Env: "e1"}}},
		Delivery: DeliveryContent{Naming: "neutral", PublishPolicy: "authorized", RetentionDays: 7},
	}
}

func env() Env {
	return Env{
		Models:         map[string]Model{"astrlink": {Categories: []string{"person", "phone", "address"}, AdapterFloor: 0.5, MemoryBytes: 200 << 20}},
		RuleCategories: map[string]bool{"phone": true}, MemoryBudgetBytes: 2 << 30,
		Formats: []string{"docx"}, FormatActions: map[string]map[string]bool{"docx": {"delete": true, "placeholder": true, "mask": true}},
	}
}

func codes(is []Issue) []string {
	var out []string
	for _, i := range is {
		out = append(out, i.Code+":"+i.Params["category"]+i.Params["model"]+i.Params["format"]+i.Params["action"])
	}
	return out
}

func TestFreezeIsDeterministicAndFacetsAreIndependent(t *testing.T) {
	a, err := Freeze(content())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Freeze(content())
	if !reflect.DeepEqual(a.Digests(), b.Digests()) || a.Version() != 1 {
		t.Fatal("equal content must give equal digests")
	}
	for _, f := range []kernel.Facet{kernel.FacetDetect, kernel.FacetDispose, kernel.FacetRender, kernel.FacetRuntime, kernel.FacetDelivery} {
		if len(a.Digest(f)) != 64 {
			t.Errorf("facet %s digest %q", f, a.Digest(f))
		}
	}
	// A change in one facet leaves the other four digests untouched (MD-09).
	changes := map[kernel.Facet]func(*Content){
		kernel.FacetDetect:  func(c *Content) { c.Detect.ModelDigest = "m2" },
		kernel.FacetDispose: func(c *Content) { c.Dispose.MinConfidence = 0.85 },
		kernel.FacetRender:  func(c *Content) { c.Render.DPI = 300 },
		kernel.FacetRuntime: func(c *Content) {
			c.Runtime.Formats["docx"] = PluginRef{ID: "tacet.docx", Version: "1", Digest: "d2", Env: "e1"}
		},
		kernel.FacetDelivery: func(c *Content) { c.Delivery.RetentionDays = 30 },
	}
	for facet, mutate := range changes {
		c := content()
		mutate(&c)
		next, changed, err := a.NewVersion(c)
		if err != nil {
			t.Fatal(err)
		}
		if next.Version() != 2 || len(changed) != 1 || changed[0] != facet {
			t.Errorf("%s: version %d changed %v", facet, next.Version(), changed)
		}
		for f, d := range a.Digests() {
			if (f == facet) == (next.Digest(f) == d) {
				t.Errorf("%s change: facet %s digest equality is wrong", facet, f)
			}
		}
	}
}

func TestMapOrderAndNamesDoNotChangeDigests(t *testing.T) {
	c1, c2 := content(), content()
	c2.Detect.LabelMap = map[string]string{"private_person": "person", "phone": "phone"}
	a, _ := Freeze(c1)
	b, _ := Freeze(c2)
	if a.Digest(kernel.FacetDetect) != b.Digest(kernel.FacetDetect) {
		t.Fatal("map insertion order changed the digest")
	}
}

func TestSnapshotIsImmutableAgainstCallerAndReaders(t *testing.T) {
	c := content()
	s, _ := Freeze(c)
	want := s.Digests()
	// Mutating the caller's input after Freeze must not reach the snapshot.
	c.Detect.LabelMap["phone"] = "tampered"
	c.Detect.Categories[0] = "tampered"
	c.Runtime.Formats["docx"] = PluginRef{ID: "evil"}
	// Neither must mutating what accessors return.
	got := s.Content()
	got.Dispose.CategoryAction["phone"] = "keep"
	got.Detect.RulePacks["p"] = "x"
	s.Digests()[kernel.FacetRender] = "x"
	j := s.FacetJSON(kernel.FacetDetect)
	j[0] = 'X'
	if !reflect.DeepEqual(s.Digests(), want) || s.Content().Dispose.CategoryAction["phone"] != "delete" ||
		s.Content().Detect.LabelMap["phone"] != "phone" || s.FacetJSON(kernel.FacetDetect)[0] != '{' {
		t.Fatal("snapshot changed through an alias")
	}
	if s.Detect().Digest != want[kernel.FacetDetect] || s.Detect().Content.ModelID != "astrlink" {
		t.Fatal("detect facet view")
	}
}

func TestNewVersionRefusesIdenticalContent(t *testing.T) {
	s, _ := Freeze(content())
	if _, _, err := s.NewVersion(content()); !errors.Is(err, ErrNoChange) {
		t.Fatalf("identical content produced a version: %v", err)
	}
}

func TestRestoreVerifiesEveryStoredDigest(t *testing.T) {
	s, _ := Freeze(content())
	back, err := Restore(3, s.Digests(), content())
	if err != nil || back.Version() != 3 || !reflect.DeepEqual(back.Digests(), s.Digests()) {
		t.Fatalf("restore: %v", err)
	}
	tampered := content()
	tampered.Render.DPI = 72
	if _, err := Restore(1, s.Digests(), tampered); !errors.Is(err, ErrNotFrozen) {
		t.Fatal("tampered content accepted")
	}
	short := s.Digests()
	delete(short, kernel.FacetDelivery)
	if _, err := Restore(1, short, content()); !errors.Is(err, ErrNotFrozen) {
		t.Fatal("missing facet accepted")
	}
	extra := s.Digests()
	extra["future"] = "x"
	if _, err := Restore(1, extra, content()); !errors.Is(err, ErrNotFrozen) {
		t.Fatal("unknown facet accepted")
	}
}

func TestCompatCleanPreset(t *testing.T) {
	r := Check(content(), env())
	if !r.Startable() || len(r.NeedsNarrowing) != 0 || len(r.Warnings) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestEveryCategoryNeedsADetectionSource(t *testing.T) {
	c := content()
	c.Detect.Categories = []string{"person", "api_key"} // nothing detects api_key
	r := Check(c, env())
	if r.Startable() || !reflect.DeepEqual(codes(r.Blocking), []string{"no_detection_source:api_key"}) {
		t.Fatalf("%v", codes(r.Blocking))
	}
	// Rules-only with a model-dependent category: narrow, never claim coverage.
	c = content()
	c.Detect.RulesOnly = true
	r = Check(c, env())
	if !r.Startable() || !reflect.DeepEqual(codes(r.NeedsNarrowing), []string{"model_dependent_category:person"}) {
		t.Fatalf("rules-only: blocking=%v narrowing=%v", codes(r.Blocking), codes(r.NeedsNarrowing))
	}
	// Rules-only without any model and a category no rule covers: blocked.
	c.Detect.ModelID = ""
	if r = Check(c, env()); r.Startable() {
		t.Fatalf("no source at all must block: %+v", r)
	}
}

func TestModelChecks(t *testing.T) {
	c := content()
	c.Detect.ModelID = "ghost"
	if r := Check(c, env()); r.Startable() || r.Blocking[0].Code != IssueModelUnknown {
		t.Fatalf("%+v", r)
	}
	e := env()
	e.MemoryBudgetBytes = 100 << 20
	if r := Check(content(), e); r.Startable() || r.Blocking[0].Code != IssueMemoryOverBudget {
		t.Fatalf("memory: %+v", r)
	}
	for name, v := range map[string]float64{"zero": 0, "below adapter floor": 0.4, "negative": -1} {
		c = content()
		c.Dispose.MinConfidence = v
		if r := Check(c, env()); r.Startable() || r.Blocking[0].Code != IssueMinConfidence {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// Rules-only needs no threshold.
	c = content()
	c.Detect.RulesOnly, c.Dispose.MinConfidence = true, 0
	c.Detect.Categories = []string{"phone"}
	if r := Check(c, env()); !r.Startable() {
		t.Fatalf("rules-only without threshold: %+v", r)
	}
}

func TestFormatActionGapsAreWarningsThatBecomeExceptions(t *testing.T) {
	e := env()
	e.FormatActions["docx"] = map[string]bool{"delete": true}
	r := Check(content(), e)
	if !r.Startable() || !reflect.DeepEqual(codes(r.Warnings), []string{"format_action_unsupported:docxplaceholder"}) {
		t.Fatalf("%v", codes(r.Warnings))
	}
}

func TestUnattendedQualification(t *testing.T) {
	c := content()
	c.Dispose.Mode = "unattended"
	c.Dispose.MinConfidence = 0.95 // above the calibrated default 0.9
	c.Runtime.Formats["docx"] = PluginRef{ID: "third.docx", Version: "1", Digest: "t1", Env: "e"}
	e := env()
	e.ThirdPartyDigests = map[string]bool{"t1": true}
	e.PackOnlyCategories = map[string]bool{"phone": true}
	r := Check(c, e)
	want := []string{"unattended_plugin_without_grant:docx", "unattended_threshold_above_default:person", "unattended_third_party_pack_only:phone"}
	var got []string
	for _, i := range r.NeedsNarrowing {
		got = append(got, i.Code+":"+i.Params["category"]+i.Params["format"])
	}
	if !r.Startable() || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// A completion grant for that exact digest lifts the plugin restriction only.
	c.Runtime.Grants = []string{"t1"}
	if r = Check(c, e); len(r.NeedsNarrowing) != 2 {
		t.Fatalf("grant: %v", codes(r.NeedsNarrowing))
	}
	// The same preset in guided mode is not narrowed.
	c.Dispose.Mode = "guided"
	if r = Check(c, e); len(r.NeedsNarrowing) != 0 {
		t.Fatalf("guided: %v", codes(r.NeedsNarrowing))
	}
}

func TestCheckIsDeterministic(t *testing.T) {
	c := content()
	c.Detect.Categories = []string{"z1", "a1", "m1"}
	a, b := Check(c, env()), Check(c, env())
	if !reflect.DeepEqual(a, b) || len(a.Blocking) != 3 || a.Blocking[0].Params["category"] != "a1" {
		t.Fatalf("%v", codes(a.Blocking))
	}
}

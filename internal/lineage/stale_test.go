package lineage

import (
	"testing"

	"github.com/jorben/tacet/internal/kernel"
)

var (
	X = kernel.StageExtractDetect
	S = kernel.StageSupplementScan
	R = kernel.StageResolve
	E = kernel.StageExport
	V = kernel.StageValidate
	U = kernel.StagePublish
)

func facets(detect, dispose, render, runtime, delivery string) map[kernel.Facet]string {
	return map[kernel.Facet]string{kernel.FacetDetect: detect, kernel.FacetDispose: dispose,
		kernel.FacetRender: render, kernel.FacetRuntime: runtime, kernel.FacetDelivery: delivery}
}

func current() Current {
	return Current{Input: "in1", Decisions: 3, SupplementTerms: 0, Facets: facets("d1", "p1", "r1", "t1", "v1")}
}

// chain is a fully valid run of every stage against current().
func chain() []Record {
	f := current().Facets
	return []Record{
		{Stage: X, Done: true, Output: 1, Input: "in1", Facets: f},
		{Stage: R, Done: true, Output: 1, Decisions: 3, Facets: f, Consumed: map[kernel.StageKind]uint64{X: 1}},
		{Stage: E, Done: true, Output: 1, Facets: f, Consumed: map[kernel.StageKind]uint64{R: 1}},
		{Stage: V, Done: true, Output: 1, Facets: f, Consumed: map[kernel.StageKind]uint64{E: 1}},
		{Stage: U, Done: true, Output: 1, Facets: f, Consumed: map[kernel.StageKind]uint64{V: 1}},
	}
}

func TestEarliestStale(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r []Record, c *Current) []Record
		want   kernel.StageKind
	}{
		{"everything valid", func(r []Record, c *Current) []Record { return r }, kernel.StageNone},
		{"input changed", func(r []Record, c *Current) []Record { c.Input = "in2"; return r }, X},
		{"detect facet changed", func(r []Record, c *Current) []Record { c.Facets[kernel.FacetDetect] = "d2"; return r }, X},
		{"runtime facet changed", func(r []Record, c *Current) []Record { c.Facets[kernel.FacetRuntime] = "t2"; return r }, X},
		{"only dispose changed reuses detection", func(r []Record, c *Current) []Record { c.Facets[kernel.FacetDispose] = "p2"; return r }, R},
		{"decisions changed", func(r []Record, c *Current) []Record { c.Decisions = 4; return r }, R},
		{"only render changed", func(r []Record, c *Current) []Record { c.Facets[kernel.FacetRender] = "r2"; return r }, E},
		{"delivery facet invalidates no processing stage", func(r []Record, c *Current) []Record { c.Facets[kernel.FacetDelivery] = "v2"; return r }, kernel.StageNone},
		{"missing facet counts as changed", func(r []Record, c *Current) []Record { delete(c.Facets, kernel.FacetRender); return r }, E},
		{"upstream rerun invalidates later stages although their own inputs match",
			func(r []Record, c *Current) []Record {
				return append(r, Record{Stage: R, Done: true, Output: 2, Decisions: 3, Facets: c.Facets, Consumed: map[kernel.StageKind]uint64{X: 1}})
			}, E},
		{"abandoned attempt is ignored", func(r []Record, c *Current) []Record {
			return append(r, Record{Stage: X, Done: false, Output: 9, Input: "other"})
		}, kernel.StageNone},
		{"stage consumed something with no finished run", func(r []Record, c *Current) []Record { return r[1:] }, R},
		{"earliest wins over later changes", func(r []Record, c *Current) []Record {
			c.Facets[kernel.FacetRender] = "r2"
			c.Decisions = 9
			return r
		}, R},
		{"no records at all", func(r []Record, c *Current) []Record { return nil }, kernel.StageNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := current()
			cur.Facets = facets("d1", "p1", "r1", "t1", "v1")
			records := c.mutate(chain(), &cur)
			if got := EarliestStale(records, cur); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSupplementScanIsOptionalButBinding(t *testing.T) {
	cur := current()
	cur.SupplementTerms = 2
	f := cur.Facets
	records := []Record{
		{Stage: X, Done: true, Output: 1, Input: "in1", Facets: f},
		{Stage: S, Done: true, Output: 1, SupplementTerms: 2, Facets: f, Consumed: map[kernel.StageKind]uint64{X: 1}},
		{Stage: R, Done: true, Output: 1, Decisions: 3, Facets: f, Consumed: map[kernel.StageKind]uint64{X: 1, S: 1}},
	}
	if got := EarliestStale(records, cur); got != kernel.StageNone {
		t.Fatalf("valid scan: %q", got)
	}
	cur.SupplementTerms = 3 // the batch term list changed
	if got := EarliestStale(records, cur); got != S {
		t.Fatalf("terms changed: %q", got)
	}
	cur.SupplementTerms = 2
	if got := EarliestStale(records[:1], cur); got != kernel.StageNone {
		t.Fatalf("scan not needed: %q", got)
	}
	// Resolution used a scan result that no longer exists.
	if got := EarliestStale([]Record{records[0], records[2]}, cur); got != R {
		t.Fatalf("resolve consumed a missing scan: %q", got)
	}
}

func TestSupersededOnlyAfterDeliveryAndNeverEditsIt(t *testing.T) {
	cur := current()
	if Superseded(chain(), cur) {
		t.Fatal("valid delivery marked superseded")
	}
	cur.Decisions = 7
	if !Superseded(chain(), cur) {
		t.Fatal("changed decisions must supersede the delivery")
	}
	if Superseded(chain()[:4], cur) {
		t.Fatal("nothing delivered, nothing to supersede")
	}
	cur = current()
	cur.Facets[kernel.FacetDelivery] = "v2" // new output settings do not retract an existing delivery
	if Superseded(chain(), cur) {
		t.Fatal("delivery facet change must not supersede a delivery")
	}
	cur.Decisions = 7
	before := chain()
	records := chain()
	EarliestStale(records, cur)
	for i := range records {
		if records[i].Output != before[i].Output || !records[i].Done {
			t.Fatal("lineage mutated its input records")
		}
	}
}

func TestFacetsChangedIsCanonicalAndSymmetric(t *testing.T) {
	a := facets("d1", "p1", "r1", "t1", "v1")
	b := facets("d1", "p2", "r1", "t2", "v1")
	got := FacetsChanged(a, b)
	if len(got) != 2 || got[0] != kernel.FacetDispose || got[1] != kernel.FacetRuntime {
		t.Fatalf("got %v", got)
	}
	if rev := FacetsChanged(b, a); len(rev) != 2 || rev[0] != got[0] || rev[1] != got[1] {
		t.Fatalf("not symmetric: %v", rev)
	}
	if len(FacetsChanged(a, facets("d1", "p1", "r1", "t1", "v1"))) != 0 {
		t.Fatal("equal facets reported as changed")
	}
	partial := map[kernel.Facet]string{kernel.FacetDetect: "d1"}
	if c := FacetsChanged(partial, a); len(c) != 4 {
		t.Fatalf("one-sided facets must count as changed: %v", c)
	}
	unknown := map[kernel.Facet]string{"future": "x"}
	if c := FacetsChanged(unknown, map[kernel.Facet]string{}); len(c) != 1 || c[0] != "future" {
		t.Fatalf("unknown facet ignored: %v", c)
	}
}

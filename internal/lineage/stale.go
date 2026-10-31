package lineage

import (
	"sort"

	"github.com/jorben/tacet/internal/kernel"
)

// Record is what lineage needs to know about one finished stage run: the
// identity of what it produced and of everything it consumed (INV-04).
type Record struct {
	Stage kernel.StageKind
	// Done is false for abandoned or in-flight attempts, which never count.
	Done bool
	// Output is the version of the artifact set this run produced; a later
	// run of the same stage has a larger value.
	Output uint64
	// Input is the input fingerprint (extraction) or decrypted-copy version.
	Input string
	// Decisions is the review decision version used by resolution.
	Decisions uint64
	// SupplementTerms is the batch supplement term version used by the scan.
	SupplementTerms uint64
	// Facets are the snapshot facet digests the stage read.
	Facets map[kernel.Facet]string
	// Consumed maps an upstream stage to the Output version this run used.
	Consumed map[kernel.StageKind]uint64
}

// Current is the live state the records are compared with.
type Current struct {
	Input           string
	Decisions       uint64
	SupplementTerms uint64
	Facets          map[kernel.Facet]string
}

// pipeline is the dependency order; publish is handled by Superseded because
// a delivery record is never invalidated, only replaced (module design 5.4).
var pipeline = []kernel.StageKind{
	kernel.StageExtractDetect, kernel.StageSupplementScan, kernel.StageResolve,
	kernel.StageExport, kernel.StageValidate,
}

// optional stages may have no record without making their dependants stale.
var optional = map[kernel.StageKind]bool{kernel.StageSupplementScan: true}

func latest(records []Record) map[kernel.StageKind]Record {
	out := map[kernel.StageKind]Record{}
	for _, r := range records {
		if !r.Done {
			continue
		}
		if prev, ok := out[r.Stage]; !ok || r.Output > prev.Output {
			out[r.Stage] = r
		}
	}
	return out
}

func facetsMatch(r Record, cur Current, stage kernel.StageKind) bool {
	for _, f := range Table[stage].Facets {
		want, ok := cur.Facets[f]
		if !ok || r.Facets[f] != want {
			return false
		}
	}
	return true
}

func stale(r Record, done map[kernel.StageKind]Record, cur Current) bool {
	if !facetsMatch(r, cur, r.Stage) {
		return true
	}
	switch r.Stage {
	case kernel.StageExtractDetect:
		if r.Input != cur.Input {
			return true
		}
	case kernel.StageSupplementScan:
		if r.SupplementTerms != cur.SupplementTerms {
			return true
		}
	case kernel.StageResolve:
		if r.Decisions != cur.Decisions {
			return true
		}
	}
	for _, up := range Table[r.Stage].Upstream {
		prev, ok := done[up]
		if !ok {
			if optional[up] {
				// A scan that was never needed is fine, but a consumed version
				// recorded for a scan that no longer exists is not.
				if _, used := r.Consumed[up]; used {
					return true
				}
				continue
			}
			return true // consumed something that has no finished run
		}
		if r.Consumed[up] != prev.Output {
			return true
		}
	}
	return false
}

// EarliestStale returns the earliest stage whose result can no longer be used.
// StageNone means every finished stage is still valid. A stage whose own
// inputs are unchanged is still stale when an upstream stage was rerun, so a
// change is never absorbed silently (INV-04).
func EarliestStale(records []Record, cur Current) kernel.StageKind {
	done := latest(records)
	for _, stage := range pipeline {
		r, ok := done[stage]
		if !ok {
			continue
		}
		if stale(r, done, cur) {
			return stage
		}
	}
	return kernel.StageNone
}

// Superseded reports whether a finished delivery no longer matches its
// upstream. The delivery itself is kept and marked replaced, never edited to
// look as if it had not happened.
func Superseded(records []Record, cur Current) bool {
	done := latest(records)
	if _, published := done[kernel.StagePublish]; !published {
		return false
	}
	return EarliestStale(records, cur) != kernel.StageNone
}

var facetOrder = []kernel.Facet{
	kernel.FacetDetect, kernel.FacetDispose, kernel.FacetRender, kernel.FacetRuntime, kernel.FacetDelivery,
}

// FacetsChanged lists the facets whose digests differ, in canonical order. A
// facet present on one side only counts as changed.
func FacetsChanged(a, b map[kernel.Facet]string) []kernel.Facet {
	var changed []kernel.Facet
	seen := map[kernel.Facet]bool{}
	for _, f := range facetOrder {
		seen[f] = true
		av, aok := a[f]
		bv, bok := b[f]
		if aok != bok || av != bv {
			changed = append(changed, f)
		}
	}
	var extra []kernel.Facet
	for f := range a {
		if !seen[f] {
			extra = append(extra, f)
		}
	}
	for f := range b {
		if !seen[f] {
			if _, dup := a[f]; !dup {
				extra = append(extra, f)
			}
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	return append(changed, extra...)
}

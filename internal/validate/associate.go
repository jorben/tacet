package validate

import (
	"errors"
	"fmt"
	"sort"

	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/kernel"
)

var ErrAssociate = errors.New("VALIDATE_ASSOCIATION_INVALID")

// Edit is one approved text operation as the candidate sees it: the span of the
// input that was replaced and how long the replacement is.
type Edit struct {
	Part    string
	Ordinal int
	Start   int // code points of the input unit
	End     int
	ReplLen int // code points of the replacement text
}

type unitKey struct {
	part    string
	ordinal int
}

// Known is a finding the input run already produced; a hit that falls inside it
// is not new, whether the person redacted it or chose to keep it.
type Known struct {
	Part       string
	Ordinal    int
	Start, End int
}

// NewFinding is a hit that exists in a candidate's retained input content,
// located at its position in the input (module design 8.2 step 4, first case).
type NewFinding struct {
	Part       string
	Ordinal    int
	Start, End int // input coordinates
	Category   string
	Source     kernel.FindingSource
	Score      float64
	Mismatch   bool
}

// Generated is a hit that lies in text the kernel's own operations produced, such
// as a placeholder or a mask. It cannot be redacted again at an input position:
// the person adjusts the operation or the output rule (second case).
type Generated struct {
	Part    string
	Ordinal int
	Start   int // candidate coordinates
	End     int
}

// Association is the outcome of locating a candidate's hits.
type Association struct {
	New       []NewFinding
	Generated []Generated
	// Unlocated are hits that could not be tied to the input or to an operation:
	// the unit is not one the plan knows or its geometry is inconsistent.
	Unlocated []extract.Hit
}

type segment struct {
	candStart, candEnd int
	inStart            int // input coordinate of candStart, kept segments only
	generated          bool
}

// segments lays the candidate unit out as alternating kept and generated runs,
// derived from the plan alone. The candidate length must agree with it, which is
// also what makes the mapping trustworthy.
func segments(edits []Edit, candChars int) ([]segment, error) {
	sorted := append([]Edit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var out []segment
	candPos, inPos := 0, 0
	for _, e := range sorted {
		if e.Start < inPos || e.End <= e.Start || e.ReplLen < 0 {
			return nil, ErrAssociate
		}
		if e.Start > inPos {
			n := e.Start - inPos
			out = append(out, segment{candPos, candPos + n, inPos, false})
			candPos += n
		}
		if e.ReplLen > 0 {
			out = append(out, segment{candPos, candPos + e.ReplLen, 0, true})
			candPos += e.ReplLen
		}
		inPos = e.End
	}
	tail := candChars - candPos
	if tail < 0 {
		return nil, ErrAssociate
	}
	if tail > 0 {
		out = append(out, segment{candPos, candPos + tail, inPos, false})
	}
	return out, nil
}

// Associate locates every hit of the candidate. Hits inside a known input finding
// are dropped; hits that span retained and generated text are split at the
// boundary and each piece is handled by its own case. Output is sorted.
func Associate(hits []extract.Hit, edits []Edit, known []Known) (Association, error) {
	byUnit := map[unitKey][]Edit{}
	for _, e := range edits {
		k := unitKey{e.Part, e.Ordinal}
		byUnit[k] = append(byUnit[k], e)
	}
	knownBy := map[unitKey][]Known{}
	for _, k := range known {
		u := unitKey{k.Part, k.Ordinal}
		knownBy[u] = append(knownBy[u], k)
	}
	var a Association
	cache := map[unitKey][]segment{}
	for _, h := range hits {
		if h.Start < 0 || h.End <= h.Start || h.End > h.Chars {
			return Association{}, fmt.Errorf("%w: hit outside its unit", ErrAssociate)
		}
		k := unitKey{h.Part, h.Ordinal}
		segs, ok := cache[k]
		if !ok {
			var err error
			if segs, err = segments(byUnit[k], h.Chars); err != nil {
				a.Unlocated = append(a.Unlocated, h)
				continue
			}
			cache[k] = segs
		}
		for _, s := range segs {
			lo, hi := max(h.Start, s.candStart), min(h.End, s.candEnd)
			if lo >= hi {
				continue
			}
			if s.generated {
				a.Generated = append(a.Generated, Generated{h.Part, h.Ordinal, lo, hi})
				continue
			}
			is, ie := s.inStart+(lo-s.candStart), s.inStart+(hi-s.candStart)
			if coveredBy(knownBy[k], is, ie) {
				continue
			}
			a.New = append(a.New, NewFinding{Part: h.Part, Ordinal: h.Ordinal, Start: is, End: ie, Category: h.Category, Source: h.Source, Score: h.Score, Mismatch: h.Mismatch})
		}
	}
	sort.Slice(a.New, func(i, j int) bool {
		x, y := a.New[i], a.New[j]
		if x.Part != y.Part {
			return x.Part < y.Part
		}
		if x.Ordinal != y.Ordinal {
			return x.Ordinal < y.Ordinal
		}
		if x.Start != y.Start {
			return x.Start < y.Start
		}
		return x.Category < y.Category
	})
	sort.Slice(a.Generated, func(i, j int) bool {
		x, y := a.Generated[i], a.Generated[j]
		if x.Part != y.Part {
			return x.Part < y.Part
		}
		if x.Ordinal != y.Ordinal {
			return x.Ordinal < y.Ordinal
		}
		return x.Start < y.Start
	})
	return a, nil
}

func coveredBy(known []Known, start, end int) bool {
	for _, k := range known {
		if k.Start <= start && end <= k.End {
			return true
		}
	}
	return false
}

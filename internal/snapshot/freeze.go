package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jorben/tacet/internal/kernel"
)

var (
	ErrNoChange  = errors.New("SNAPSHOT_NO_CHANGE")
	ErrNotFrozen = errors.New("SNAPSHOT_INVALID")
)

// Facet is one frozen facet: its canonical encoding and the digest of it.
type Facet struct {
	Digest string
	JSON   []byte
}

// Snapshot is immutable. Fields are unexported and accessors copy, so a stage
// cannot alter what a later stage or a resumed task will read (INV-03).
type Snapshot struct {
	version uint64
	facets  map[kernel.Facet]Facet
	content Content
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func encode(v any) (Facet, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Facet{}, ErrNotFrozen
	}
	return Facet{Digest: digest(b), JSON: b}, nil
}

func facetsOf(c Content) (map[kernel.Facet]Facet, error) {
	parts := []struct {
		f kernel.Facet
		v any
	}{
		{kernel.FacetDetect, c.Detect}, {kernel.FacetDispose, c.Dispose}, {kernel.FacetRender, c.Render},
		{kernel.FacetRuntime, c.Runtime}, {kernel.FacetDelivery, c.Delivery},
	}
	out := make(map[kernel.Facet]Facet, len(parts))
	for _, p := range parts {
		f, err := encode(p.v)
		if err != nil {
			return nil, err
		}
		out[p.f] = f
	}
	return out, nil
}

// clone deep-copies through the canonical encoding.
func clone(c Content) (Content, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return Content{}, ErrNotFrozen
	}
	var out Content
	if err := json.Unmarshal(b, &out); err != nil {
		return Content{}, ErrNotFrozen
	}
	return out, nil
}

// Freeze builds version 1. The caller must have run Check and obtained any
// narrowing confirmation first; Freeze does not decide compatibility.
func Freeze(c Content) (*Snapshot, error) { return build(1, c) }

func build(version uint64, c Content) (*Snapshot, error) {
	own, err := clone(c) // detach from the caller's maps and slices
	if err != nil {
		return nil, err
	}
	facets, err := facetsOf(own)
	if err != nil {
		return nil, err
	}
	return &Snapshot{version: version, facets: facets, content: own}, nil
}

func (s *Snapshot) Version() uint64 { return s.version }

// Digest of one facet. Unknown facets return "".
func (s *Snapshot) Digest(f kernel.Facet) string { return s.facets[f].Digest }

// Digests returns all facet digests, for lineage comparisons.
func (s *Snapshot) Digests() map[kernel.Facet]string {
	out := make(map[kernel.Facet]string, len(s.facets))
	for f, v := range s.facets {
		out[f] = v.Digest
	}
	return out
}

// Content returns a copy; mutating it never changes the snapshot.
func (s *Snapshot) Content() Content {
	c, _ := clone(s.content)
	return c
}

// FacetJSON returns the canonical encoding of one facet, for persistence.
func (s *Snapshot) FacetJSON(f kernel.Facet) []byte {
	return append([]byte(nil), s.facets[f].JSON...)
}

// Detect returns the detect facet view handed to detection.
func (s *Snapshot) Detect() DetectFacet {
	return DetectFacet{Digest: s.facets[kernel.FacetDetect].Digest, Content: s.Content().Detect}
}

var facetOrder = []kernel.Facet{
	kernel.FacetDetect, kernel.FacetDispose, kernel.FacetRender, kernel.FacetRuntime, kernel.FacetDelivery,
}

// NewVersion freezes changed content as the next version and reports which
// facets differ, in canonical order. An identical content is ErrNoChange: it
// never creates a version that would needlessly invalidate results (INV-04).
func (s *Snapshot) NewVersion(c Content) (*Snapshot, []kernel.Facet, error) {
	next, err := build(s.version+1, c)
	if err != nil {
		return nil, nil, err
	}
	var changed []kernel.Facet
	for _, f := range facetOrder {
		if next.facets[f].Digest != s.facets[f].Digest {
			changed = append(changed, f)
		}
	}
	if len(changed) == 0 {
		return nil, nil, ErrNoChange
	}
	return next, changed, nil
}

// Restore rebuilds a stored snapshot and verifies every stored digest against
// the content, so a tampered or partially written record is refused (INV-02).
func Restore(version uint64, stored map[kernel.Facet]string, c Content) (*Snapshot, error) {
	s, err := build(version, c)
	if err != nil {
		return nil, err
	}
	if len(stored) != len(facetOrder) {
		return nil, ErrNotFrozen
	}
	for _, f := range facetOrder {
		if want, ok := stored[f]; !ok || want != s.facets[f].Digest {
			return nil, ErrNotFrozen
		}
	}
	return s, nil
}

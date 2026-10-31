package extract

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/kernel"
)

// ErrRescanLimit is returned when a candidate yields more hits than the
// in-memory re-detection keeps; the validation is then not run, never trimmed.
var ErrRescanLimit = errors.New("EXTRACT_RESCAN_LIMIT")

// MaxRescanHits bounds the hits one candidate scan holds.
var MaxRescanHits = 200_000

// Hit is one detection in a candidate's text, addressed by the plugin's source
// map (part, ordinal) and by code points inside that unit. Nothing about the
// value is kept.
type Hit struct {
	Part, Visibility string
	Ordinal          int
	Chars            int // length of the unit's text
	Start, End       int
	Category         string
	Source           kernel.FindingSource
	Score            float64
	Mismatch         bool
}

// ScanResult is a candidate's re-detection: hits, the parts the plugin reported
// and whether each part was read in full.
type ScanResult struct {
	Hits  []Hit
	Units int
	Parts map[string]string // part -> state reported by the plugin
	Stats detect.Stats
}

// Rescan runs the extraction method on a candidate and detects with the same
// facet as the original run (module design 8.2 step 3, MD-13). It is the
// pipeline without storage: no unit, finding or ledger row is written, so a
// candidate that is later thrown away leaves nothing behind. Completeness is
// judged exactly as in Run.
func (p *Pipeline) Rescan(ctx context.Context, req Request) (ScanResult, error) {
	res := ScanResult{Parts: map[string]string{}}
	var declared *struct{ Units, Parts int }
	commit := func(ctx context.Context, raw json.RawMessage) error {
		if p.Schemas.Validate("format.schema.json", "ExtractItem", raw) != nil || declared != nil {
			return ErrProtocol
		}
		var head struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &head) != nil {
			return ErrProtocol
		}
		switch head.Kind {
		case "text_unit":
			var u struct {
				UnitID     string `json:"unit_id"`
				Part       string
				Ordinal    int
				Text       string
				Visibility string
			}
			if json.Unmarshal(raw, &u) != nil || !utf8.ValidString(u.Text) {
				return ErrProtocol
			}
			n := utf8.RuneCountInString(u.Text)
			if n < 1 || n > MaxUnitChars {
				return ErrProtocol
			}
			det, err := p.Detector.DetectText(ctx, detect.Unit{Span: kernel.Span{UnitID: u.UnitID, TextVersion: 1}, Body: u.Text}, req.Facet)
			if err != nil {
				return err
			}
			for _, c := range det.Candidates {
				if len(res.Hits) >= MaxRescanHits {
					return ErrRescanLimit
				}
				res.Hits = append(res.Hits, Hit{Part: u.Part, Ordinal: u.Ordinal, Visibility: u.Visibility, Chars: n, Start: c.Span.Start, End: c.Span.End,
					Category: c.Category, Source: c.Source, Score: c.Score, Mismatch: c.ValidatorMismatch})
			}
			res.Units++
			res.Stats.Model += det.Stats.Model
			res.Stats.Rule += det.Stats.Rule
			return nil
		case "part_state":
			var s struct{ Part, State string }
			if json.Unmarshal(raw, &s) != nil {
				return ErrProtocol
			}
			if _, dup := res.Parts[s.Part]; dup {
				return ErrProtocol
			}
			res.Parts[s.Part] = s.State
			return nil
		case "extract_complete":
			var c struct{ Units, Parts int }
			if json.Unmarshal(raw, &c) != nil {
				return ErrProtocol
			}
			declared = &c
			return nil
		}
		return ErrProtocol
	}
	method := req.Method
	if method == "" {
		method = "format.extract"
	}
	outcome, err := req.Session.Stream(ctx, method, req.Attempt, req.Params, commit)
	if err == nil && (outcome.Status != "completed" || declared == nil || declared.Units != res.Units || declared.Parts != len(res.Parts) || len(res.Parts) == 0) {
		err = ErrIncomplete
	}
	if err != nil {
		return ScanResult{}, err
	}
	return res, nil
}

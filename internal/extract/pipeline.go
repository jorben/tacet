package extract

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

var (
	ErrProtocol   = errors.New("EXTRACT_PROTOCOL_VIOLATION")
	ErrIncomplete = errors.New("EXTRACT_INCOMPLETE")
	ErrPipeline   = errors.New("EXTRACT_CONFIG_INVALID")
)

// MaxUnitChars is the text unit bound of requirements 5.4 (1 MiB of code points).
const MaxUnitChars = 1 << 20

// Pipeline is the extraction-and-detection stage as a library function. It does
// not depend on task state: the extraction stage runs it on the input and
// validation runs it on a candidate (module design 6.2, 8.2).
type Pipeline struct {
	WS       *workspace.Workspace
	Detector *detect.Service
	Findings *findings.Repository
	Coverage coverage.Repository
	Units    Repository
	Bodies   Bodies
	Schemas  *host.Schemas
}

// Request is one run. Params carries the plugin's input reference; Batch scopes
// the same-value grouping key.
type Request struct {
	Session *host.Session
	Attempt kernel.AttemptRef
	Batch   string
	Method  string // "format.extract"
	Params  map[string]any
	Facet   snapshot.DetectFacet
}

// Result summarises a completed run.
type Result struct {
	Units         int
	Parts         int
	Gaps          int // parts whose state is not "inspected"
	Candidates    int
	ResultVersion uint64
	Stats         detect.Stats
}

func New(p Pipeline) (*Pipeline, error) {
	if p.WS == nil || p.Detector == nil || p.Findings == nil || p.Bodies == nil {
		return nil, ErrPipeline
	}
	schemas, err := host.NewSchemas()
	if err != nil {
		return nil, err
	}
	if err = schemas.Compile("format.schema.json", "ExtractItem"); err != nil {
		return nil, err
	}
	p.Schemas = schemas
	return &p, nil
}

func partOf(state, class string) coverage.Part {
	p := coverage.Part{Disposition: coverage.Pending, Validation: coverage.NotRun, Bounded: true}
	switch class {
	case "text":
		p.Class, p.Required, p.Known = coverage.ClassText, true, true
	case "other":
		p.Class, p.Known = coverage.ClassOther, true
	default: // unknown to the plugin: a gap whether or not anything refers to it
		p.Class, p.Known = coverage.ClassOther, false
	}
	switch state {
	case "inspected":
		p.Inspection = coverage.Inspected
	case "not_inspected":
		p.Inspection = coverage.NotInspected
	case "unsupported":
		p.Inspection = coverage.Unsupported
	default:
		p.Inspection = coverage.InspectFail
	}
	return p
}

func runes(s string, start, end int) []byte {
	r := []rune(s)
	if start < 0 || end > len(r) || start >= end {
		return nil
	}
	return []byte(string(r[start:end]))
}

// Run drives one extraction. The plugin is credited again only after the unit's
// text, findings and coverage are committed (the Stream commit callback), and
// the run counts as complete only if the stream ended completed, the plugin sent
// its completion marker, and its declared counts equal what was committed.
// Any other end discards the attempt's units and findings; the coverage ledger
// stays incomplete, so it can never be read as "nothing to check".
func (p *Pipeline) Run(ctx context.Context, req Request) (Result, error) {
	a := req.Attempt
	if err := p.WS.Commit(ctx, func(tx store.Tx) error {
		if err := p.WS.AdmitTx(tx, a); err != nil {
			return err
		}
		return p.Coverage.Begin(tx, a.JobID, a.AttemptID)
	}); err != nil {
		return Result{}, err
	}
	var res Result
	var declared *struct{ Units, Parts int }
	seq := 0
	parts := map[string]bool{}
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
			return p.unit(ctx, req, raw, &seq, &res)
		case "part_state":
			var s struct {
				Part, State, Class string
			}
			if json.Unmarshal(raw, &s) != nil || parts[s.Part] {
				return ErrProtocol // each part is reported once
			}
			parts[s.Part] = true
			part := partOf(s.State, s.Class)
			part.ID = s.Part
			if part.Inspection != coverage.Inspected {
				res.Gaps++
			}
			return p.WS.Commit(ctx, func(tx store.Tx) error {
				if err := p.WS.AdmitTx(tx, a); err != nil {
					return err
				}
				return p.Coverage.Record(tx, a.JobID, a.AttemptID, part)
			})
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
	outcome, err := req.Session.Stream(ctx, method, a, req.Params, commit)
	if err == nil && (outcome.Status != "completed" || declared == nil || declared.Units != seq || declared.Parts != len(parts) || len(parts) == 0) {
		err = ErrIncomplete
	}
	if err != nil {
		p.discard(a)
		return Result{}, err
	}
	res.Units, res.Parts = seq, len(parts)
	if err = p.WS.Commit(ctx, func(tx store.Tx) error {
		if err := p.WS.AdmitTx(tx, a); err != nil {
			return err
		}
		if _, err := p.Units.Seal(tx, a.JobID, a.AttemptID, seq); err != nil {
			return err
		}
		if err := p.Coverage.Complete(tx, a.JobID, a.AttemptID); err != nil {
			return err
		}
		v, err := p.Findings.Seal(tx, a, req.Batch, findings.StageDetect)
		res.ResultVersion = v
		return err
	}); err != nil {
		p.discard(a)
		return Result{}, err
	}
	return res, nil
}

// unit validates, detects and commits one text unit.
func (p *Pipeline) unit(ctx context.Context, req Request, raw json.RawMessage, seq *int, res *Result) error {
	var u struct {
		UnitID     string `json:"unit_id"`
		Part       string
		Ordinal    int
		Text       string
		Visibility string
		Location   Location
	}
	if json.Unmarshal(raw, &u) != nil || !utf8.ValidString(u.Text) {
		return ErrProtocol
	}
	n := utf8.RuneCountInString(u.Text)
	if n < 1 || n > MaxUnitChars {
		return ErrProtocol
	}
	a := req.Attempt
	// detection happens outside the commit section: it may take long and calls a plugin
	det, err := p.Detector.DetectText(ctx, detect.Unit{Span: kernel.Span{UnitID: u.UnitID, TextVersion: 1}, Body: u.Text}, req.Facet)
	if err != nil {
		return err
	}
	items := make([]findings.New, 0, len(det.Candidates))
	for _, c := range det.Candidates {
		items = append(items, findings.New{Unit: u.UnitID, TextVersion: c.Span.TextVersion, Start: c.Span.Start, End: c.Span.End,
			Category: c.Category, Source: c.Source, Score: c.Score, LowConf: c.LowConf, Mismatch: c.ValidatorMismatch,
			RuleID: c.RuleID, Template: u.Part, Value: runes(u.Text, c.Span.Start, c.Span.End)})
	}
	bodyID := "b_" + a.AttemptID + "_" + itoa(*seq+1)
	if err = p.Bodies.Put(bodyID, u.Text); err != nil {
		return err
	}
	err = p.WS.Commit(ctx, func(tx store.Tx) error {
		if err := p.WS.AdmitTx(tx, a); err != nil {
			return err
		}
		if err := p.Units.Add(tx, a.JobID, Unit{Attempt: a.AttemptID, Seq: *seq + 1, UnitID: u.UnitID, Part: u.Part, Ordinal: u.Ordinal,
			Visibility: u.Visibility, Chars: n, Body: bodyID, Location: u.Location}); err != nil {
			return err
		}
		_, err := p.Findings.Add(tx, a, req.Batch, findings.StageDetect, items)
		return err
	})
	if err != nil {
		p.Bodies.Delete(bodyID)
		return err
	}
	*seq++
	res.Candidates += len(det.Candidates)
	res.Stats.Model += det.Stats.Model
	res.Stats.Rule += det.Stats.Rule
	res.Stats.UnmappedLabels += det.Stats.UnmappedLabels
	return nil
}

// discard removes what an unfinished attempt left. Best effort by design: it
// runs after a failure, and a leftover is swept by retention, never read.
func (p *Pipeline) discard(a kernel.AttemptRef) {
	ctx := context.Background()
	var ids []string
	p.WS.Commit(ctx, func(tx store.Tx) error {
		var err error
		if ids, err = p.Units.Discard(tx, a.AttemptID); err != nil {
			return err
		}
		return p.Findings.Discard(tx, a.AttemptID)
	})
	for _, id := range ids {
		p.Bodies.Delete(id)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

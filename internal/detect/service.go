package detect

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/snapshot"
)

var (
	ErrModelUnavailable = errors.New("DETECT_MODEL_UNAVAILABLE")
	ErrModelOutput      = errors.New("DETECT_MODEL_OUTPUT_INVALID") // protocol error: out of range, non-finite
	ErrUnit             = errors.New("DETECT_UNIT_INVALID")
	ErrConfig           = errors.New("DETECT_CONFIG_INVALID")
)

// Unit is one text unit as detection sees it. Prefix and Suffix are synthetic
// context (a table header, neighbouring paragraphs) that helps the model but is
// not part of the document body: nothing found there may become a candidate
// (FR-15). Field is the structured field path or column, empty for plain text.
type Unit struct {
	Ref    kernel.ArtifactRef
	Span   kernel.Span // UnitID and TextVersion of the body; Start/End are ignored
	Prefix string
	Body   string
	Suffix string
	Field  string
}

// UnitLoader fetches unit text; detect itself stores nothing.
type UnitLoader interface {
	Load(ctx context.Context, ref kernel.ArtifactRef) (Unit, error)
}

// ModelSpan is one labelled span returned by a model adapter, in code points of
// the text it was given.
type ModelSpan struct {
	Label      string
	Start, End int
	Score      float64
}

// ModelRunner is the detection plugin's model adapter. The adapter owns
// tokenisation, windowing, overlap and decoding.
type ModelRunner interface {
	Run(ctx context.Context, text string) ([]ModelSpan, error)
}

// Stats counts what normalisation did, so nothing is lost silently.
type Stats struct {
	UnmappedLabels    int // model labels with no Tacet category: counted, no candidate
	DisabledCategory  int // candidates of a category the run does not enable
	BelowAdapterFloor int
	ClippedToBody     int // candidates partly in synthetic context, cut back to the body
	InContextOnly     int // candidates entirely in synthetic context, dropped
	FieldRule         int // whole-value candidates (counted apart from model findings)
	Model, Rule       int
}

// Config wires a Service. Rules are compiled once; the dictionary set tells
// dictionary hits from pattern hits.
type Config struct {
	Loader UnitLoader
	Rules  []rules.Rule
	Budget rules.Budget
	Model  ModelRunner // nil in rules-only mode
	// FieldRules maps a field path to the category its whole value belongs to
	// (FR-17). nil means no field rules.
	FieldRules func(field string) (category string, ok bool)
}

type Service struct {
	cfg  Config
	set  *rules.Set
	dict map[string]bool
}

func New(cfg Config) (*Service, error) {
	if cfg.Loader == nil {
		return nil, ErrConfig
	}
	budget := cfg.Budget
	if budget == (rules.Budget{}) {
		budget = rules.DefaultBudget()
	}
	set, err := rules.Compile(cfg.Rules, budget)
	if err != nil {
		return nil, err
	}
	dict := map[string]bool{}
	for _, r := range cfg.Rules {
		if len(r.Terms) > 0 {
			dict[r.ID] = true
		}
	}
	return &Service{cfg: cfg, set: set, dict: dict}, nil
}

var _ Detector = (*Service)(nil)

func enabled(f snapshot.DetectFacet) map[string]bool {
	m := make(map[string]bool, len(f.Content.Categories))
	for _, c := range f.Content.Categories {
		m[c] = true
	}
	return m
}

// UnitResult is the outcome for one unit.
type UnitResult struct {
	Candidates []Candidate
	Stats      Stats
}

// DetectUnit runs rules and (unless rules-only) the model over one unit. Both
// must succeed; a failure of either returns an error and no candidates (INV-02).
func (s *Service) DetectUnit(ctx context.Context, ref kernel.ArtifactRef, facet snapshot.DetectFacet) (UnitResult, error) {
	unit, err := s.cfg.Loader.Load(ctx, ref)
	if err != nil {
		return UnitResult{}, err
	}
	return s.DetectText(ctx, unit, facet)
}

// DetectText is DetectUnit for a unit the caller already holds, as the extract
// pipeline does while it streams: nothing is loaded and nothing is stored.
func (s *Service) DetectText(ctx context.Context, unit Unit, facet snapshot.DetectFacet) (UnitResult, error) {
	if unit.Span.UnitID == "" {
		return UnitResult{}, ErrUnit
	}
	if !utf8.ValidString(unit.Prefix) || !utf8.ValidString(unit.Body) || !utf8.ValidString(unit.Suffix) {
		return UnitResult{}, ErrUnit
	}
	var out UnitResult
	bodyLen := utf8.RuneCountInString(unit.Body)
	prefixLen := utf8.RuneCountInString(unit.Prefix)
	on := enabled(facet)
	body := kernel.Span{UnitID: unit.Span.UnitID, TextVersion: unit.Span.TextVersion}

	if s.cfg.FieldRules != nil && unit.Field != "" {
		if category, ok := s.cfg.FieldRules(unit.Field); ok {
			if !on[category] {
				out.Stats.DisabledCategory++
				return out, nil
			}
			if bodyLen > 0 { // an empty value has nothing to cover
				sp := body
				sp.Start, sp.End = 0, bodyLen
				out.Candidates = append(out.Candidates, Candidate{Span: sp, Category: category, Source: kernel.SourceFieldRule})
				out.Stats.FieldRule++
			}
			return out, nil // the whole value is covered: neither rules nor model add information
		}
	}

	text := unit.Prefix + unit.Body + unit.Suffix
	total := prefixLen + bodyLen + utf8.RuneCountInString(unit.Suffix)
	useModel := !facet.Content.RulesOnly && facet.Content.ModelID != ""
	if useModel && s.cfg.Model == nil {
		return UnitResult{}, ErrModelUnavailable
	}

	var (
		wg      sync.WaitGroup
		hits    []rules.Hit
		spans   []ModelSpan
		hitErr  error
		modErr  error
		cctx, c = context.WithCancel(ctx)
	)
	defer c()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if hits, hitErr = s.set.Match(cctx, text); hitErr != nil {
			c() // the unit is failed either way; stop the other source early
		}
	}()
	if useModel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if spans, modErr = s.cfg.Model.Run(cctx, text); modErr != nil {
				c()
			}
		}()
	}
	wg.Wait()
	// report the first real cause, not the cancellation it triggered in the other source
	for _, err := range []error{hitErr, modErr} {
		if err != nil && !errors.Is(err, context.Canceled) {
			return UnitResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return UnitResult{}, err
	}
	if hitErr != nil || modErr != nil {
		return UnitResult{}, context.Canceled
	}

	place := func(start, end int, category string, source kernel.FindingSource) (Candidate, bool) {
		if !on[category] {
			out.Stats.DisabledCategory++
			return Candidate{}, false
		}
		start -= prefixLen
		end -= prefixLen
		if start >= bodyLen || end <= 0 || start == end {
			out.Stats.InContextOnly++
			return Candidate{}, false
		}
		if start < 0 || end > bodyLen {
			out.Stats.ClippedToBody++
			start, end = max(start, 0), min(end, bodyLen)
		}
		sp := body
		sp.Start, sp.End = start, end
		return Candidate{Span: sp, Category: category, Source: source}, true
	}

	for _, h := range hits {
		if h.Start < 0 || h.End > total || h.Start > h.End {
			return UnitResult{}, ErrUnit
		}
		source := kernel.SourceRule
		if s.dict[h.RuleID] {
			source = kernel.SourceDictionary
		}
		if c, ok := place(h.Start, h.End, h.Category, source); ok {
			c.RuleID, c.ValidatorMismatch = h.RuleID, h.ValidatorMismatch
			out.Candidates = append(out.Candidates, c)
			out.Stats.Rule++
		}
	}
	for _, m := range spans {
		if m.Start < 0 || m.End > total || m.Start >= m.End || math.IsNaN(m.Score) || math.IsInf(m.Score, 0) {
			return UnitResult{}, ErrModelOutput // never clipped or repaired: a wrong span means wrong coordinates
		}
		category, mapped := facet.Content.LabelMap[m.Label]
		if !mapped {
			out.Stats.UnmappedLabels++
			continue
		}
		if m.Score < facet.Content.AdapterFloor {
			out.Stats.BelowAdapterFloor++
			continue
		}
		if c, ok := place(m.Start, m.End, category, kernel.SourceModel); ok {
			c.Score = m.Score
			c.LowConf = m.Score < facet.Content.Calibrated
			out.Candidates = append(out.Candidates, c)
			out.Stats.Model++
		}
	}
	sortCandidates(out.Candidates)
	return out, nil
}

func sortCandidates(c []Candidate) {
	sort.SliceStable(c, func(i, j int) bool {
		a, b := c[i], c[j]
		switch {
		case a.Span.UnitID != b.Span.UnitID:
			return a.Span.UnitID < b.Span.UnitID
		case a.Span.Start != b.Span.Start:
			return a.Span.Start < b.Span.Start
		case a.Span.End != b.Span.End:
			return a.Span.End < b.Span.End
		case a.Source != b.Source:
			return a.Source < b.Source
		}
		return a.Category < b.Category
	})
}

// DetectUnits implements Detector over several units; the first failure fails
// the whole call and no partial candidates are returned.
func (s *Service) DetectUnits(ctx context.Context, units []kernel.ArtifactRef, facet snapshot.DetectFacet) ([]Candidate, error) {
	var all []Candidate
	for _, ref := range units {
		r, err := s.DetectUnit(ctx, ref, facet)
		if err != nil {
			return nil, err
		}
		all = append(all, r.Candidates...)
	}
	return all, nil
}

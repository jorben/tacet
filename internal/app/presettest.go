package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
)

// MaxTestText bounds the text of a preset test, in characters.
const MaxTestText = 20000

// TestHit is one candidate a preset finds in a test text. Start and End count UTF-16 code units of the
// text, so a JavaScript front end can slice it directly.
type TestHit struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Category string `json:"category"`
	// Source is "model", "rule" or "dictionary" (a word-list rule).
	Source string `json:"source"`
	// Rule and RuleName name the rule that matched; empty for model candidates.
	Rule     string  `json:"rule,omitempty"`
	RuleName string  `json:"ruleName,omitempty"`
	Score    float64 `json:"score,omitempty"`
	// Low: below the model's calibrated value. Below: below the preset's minimum confidence, so a task
	// would keep it and not redact it.
	Low   bool `json:"low,omitempty"`
	Below bool `json:"below,omitempty"`
}

// PresetTest is what a preset finds in a text, as a task with this preset would detect it. Nothing is
// stored: the text and the candidates live only for the call.
type PresetTest struct {
	Hits          []TestHit `json:"hits"`
	RulesOnly     bool      `json:"rulesOnly"`
	Model         string    `json:"model,omitempty"`
	MinConfidence float64   `json:"minConfidence,omitempty"`
}

var presetTestSeq atomic.Uint64

// TestPreset runs the detection of a stored preset over text: its rule sets and, unless it is rules
// only, its model with the minimum confidence it sets. A model that is not installed fails with
// MODEL_NOT_INSTALLED; the model waits for the slot a running task may hold.
func (e *Engine) TestPreset(ctx context.Context, preset, text string) (PresetTest, error) {
	if preset == "" || text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxTestText {
		return PresetTest{}, ErrInvalid
	}
	refs := map[string]snapshot.PluginRef{}
	for f, p := range e.plugins {
		refs[f] = p.id
	}
	if len(refs) == 0 {
		return PresetTest{}, ErrNotReady
	}
	avail := e.available(ctx)
	var content snapshot.Content
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		var err error
		content, err = e.resolveContent(tx, preset, refs, "", avail)
		return err
	})
	switch {
	case errors.Is(err, ErrModelNotInstalled):
		return PresetTest{}, ErrModelNotInstalled
	case errors.Is(err, snapshot.ErrBinding):
		return PresetTest{}, ErrInvalid
	case err != nil:
		return PresetTest{}, mapConfigErr(err)
	}
	snap, err := snapshot.Freeze(content)
	if err != nil {
		return PresetTest{}, err
	}
	useModel := content.Detect.ModelID != "" && !content.Detect.RulesOnly
	var det *detect.Service
	if useModel {
		n := presetTestSeq.Add(1)
		a := kernel.AttemptRef{JobID: "preset_test", AttemptID: fmt.Sprintf("preset_test_%d", n), OwnerGeneration: e.ws.Generation(), JobVersion: 1}
		runner, release, err := e.modelRunner(ctx, a, content.Detect.ModelID, content.Detect.Execution)
		if err != nil {
			return PresetTest{}, err
		}
		defer release()
		if det, err = e.buildDetector(content, runner); err != nil {
			return PresetTest{}, err
		}
	} else if det, err = e.detector(content); err != nil {
		return PresetTest{}, err
	}
	res, err := det.DetectText(ctx, detect.Unit{Span: kernel.Span{UnitID: "preset_test", TextVersion: 1}, Body: text}, snap.Detect())
	if err != nil {
		return PresetTest{}, err
	}
	names := map[string]string{}
	var rc config.RulesContent
	if jsonUnmarshal(content.Detect.RuleContent, &rc) == nil {
		for _, r := range rc.Rules {
			names[r.ID] = r.Name
		}
	}
	out := PresetTest{Hits: make([]TestHit, 0, len(res.Candidates)), RulesOnly: !useModel}
	if useModel {
		out.Model, out.MinConfidence = content.Detect.ModelID, content.Dispose.MinConfidence
	}
	for _, c := range res.Candidates {
		at := utf16Offsets(text, c.Span.Start, c.Span.End)
		h := TestHit{Start: at[0], End: at[1], Category: c.Category, Source: string(c.Source), Rule: c.RuleID, RuleName: names[c.RuleID]}
		if c.Source == kernel.SourceModel {
			h.Score, h.Low = c.Score, c.LowConf
			h.Below = out.MinConfidence > 0 && c.Score < out.MinConfidence
		}
		out.Hits = append(out.Hits, h)
	}
	return out, nil
}

package detect

import (
	"context"
	"encoding/json"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
)

// PluginModel runs the model adapter living in a detection plugin process over
// the plugin protocol (detect.schema.json). Calls are sequential: a Session
// carries one task at a time.
type PluginModel struct {
	session *host.Session
	attempt func() kernel.AttemptRef
	schemas *host.Schemas
}

// NewPluginModel needs a session that has completed Initialize, and a source of
// attempt identities: every call is its own attempt, so a late result of one
// call cannot be taken for another's.
func NewPluginModel(session *host.Session, attempt func() kernel.AttemptRef) (*PluginModel, error) {
	if session == nil || attempt == nil {
		return nil, ErrConfig
	}
	schemas, err := host.NewSchemas()
	if err != nil {
		return nil, err
	}
	for _, def := range []string{"Params", "Item"} {
		if err = schemas.Compile("detect.schema.json", def); err != nil {
			return nil, err
		}
	}
	return &PluginModel{session: session, attempt: attempt, schemas: schemas}, nil
}

// Run returns every span the plugin reported for text. The result counts as
// complete only if the plugin ended the stream as completed and its unit_done
// count equals the spans received: a dropped batch must not look like "nothing
// found" (INV-02).
func (p *PluginModel) Run(ctx context.Context, text string) ([]ModelSpan, error) {
	var spans []ModelSpan
	done, reported := false, 0
	outcome, err := p.session.Stream(ctx, "detect.run", p.attempt(), map[string]any{"text": text},
		func(_ context.Context, raw json.RawMessage) error {
			if p.schemas.Validate("detect.schema.json", "Item", raw) != nil || done {
				return ErrModelOutput
			}
			var kind struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &kind) != nil {
				return ErrModelOutput
			}
			switch kind.Kind {
			case "unit_done":
				var end struct {
					Spans int `json:"spans"`
				}
				if json.Unmarshal(raw, &end) != nil {
					return ErrModelOutput
				}
				done, reported = true, end.Spans
			case "spans":
				var batch struct {
					Spans []struct {
						Label string  `json:"label"`
						Start int     `json:"start"`
						End   int     `json:"end"`
						Score float64 `json:"score"`
					} `json:"spans"`
				}
				if json.Unmarshal(raw, &batch) != nil {
					return ErrModelOutput
				}
				for _, s := range batch.Spans {
					spans = append(spans, ModelSpan{Label: s.Label, Start: s.Start, End: s.End, Score: s.Score})
				}
			default:
				return ErrModelOutput
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	if outcome.Status != "completed" {
		return nil, ErrModelUnavailable
	}
	if !done || reported != len(spans) {
		return nil, ErrModelOutput
	}
	return spans, nil
}

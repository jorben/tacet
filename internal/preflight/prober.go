package preflight

import (
	"context"
	"encoding/json"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

// PluginProber runs the budgeted format.probe on one worker and returns the
// plugin's single probe_result. The plugin's verdict is evidence for Decide, not
// a decision: an unknown format or a value outside the bounds is refused there.
type PluginProber struct {
	Session *host.Session
	Attempt kernel.AttemptRef
	Input   refs.Wire
}

func (p *PluginProber) Probe(ctx context.Context) (Probe, error) {
	if p.Session == nil || p.Input.Mode != refs.Read {
		return Probe{}, ErrProbe
	}
	var got struct {
		Kind       string     `json:"kind"`
		Format     string     `json:"format"`
		Encryption Encryption `json:"encryption"`
		Pages      int        `json:"pages"`
		Images     int        `json:"images"`
		Embedded   int        `json:"embedded"`
		Bytes      int64      `json:"bytes"`
	}
	seen := 0
	out, err := p.Session.Stream(ctx, "format.probe", p.Attempt, map[string]any{"input": p.Input},
		func(_ context.Context, item json.RawMessage) error {
			seen++
			if seen > 1 || json.Unmarshal(item, &got) != nil || got.Kind != "probe_result" {
				return ErrProbe
			}
			return nil
		})
	if err != nil || out.Status != "completed" || seen != 1 {
		return Probe{}, ErrProbe
	}
	return Probe{Format: got.Format, Encryption: got.Encryption, Pages: got.Pages, Images: got.Images, Embedded: got.Embedded, Bytes: got.Bytes}, nil
}

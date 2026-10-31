package review

import (
	"context"
	"errors"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// ManualRequest is a finding the user selected or boxed. The position was
// validated by preview against the unit's text version before this call.
type ManualRequest struct {
	Request  string
	Batch    string
	Job      string
	Unit     string
	Version  uint64
	Start    int
	End      int
	Category string
	Value    []byte // matched text, only to compute the same-value HMAC; cleared
	Field    string
	Template string
}

// AddManual adds the finding and a single redact decision for it in one commit
// (FR-22): either the user's selection exists with its decision, or neither does.
// Retrying the same request returns the same finding and decision.
func (s *Service) AddManual(ctx context.Context, r ManualRequest) (finding int64, decision string, err error) {
	defer clear(r.Value)
	if !s.ok() || !validRequest(r.Request) || r.Batch == "" || r.Job == "" || r.Category == "" {
		return 0, "", ErrInvalid
	}
	err = s.Commit(ctx, func(tx store.Tx) error {
		id, _, err := s.Findings.AddManual(tx, r.Job, r.Batch, r.Request, findings.New{Unit: r.Unit, TextVersion: r.Version, Start: r.Start, End: r.End,
			Category: r.Category, Source: kernel.SourceManual, Score: 1, Field: r.Field, Template: r.Template, Value: r.Value})
		if err != nil {
			return err
		}
		finding = id
		decision, err = s.decideItem(tx, ItemRequest{Request: "manual:" + r.Request, Batch: r.Batch, Job: r.Job, Finding: id, Keep: false})
		return err
	})
	switch {
	case err == nil:
		return finding, decision, nil
	case errors.Is(err, findings.ErrInvalid), errors.Is(err, ErrInvalid):
		return 0, "", ErrInvalid
	}
	return 0, "", wrap(err)
}

package review

import (
	"context"
	"errors"
	"fmt"

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

// WithdrawManual undoes a manual redaction (FR-22): the finding is withdrawn and the redact decision that
// came with it is revoked, in one commit, so neither the selection nor its decision stays. Only a manual
// finding can be withdrawn; the history of the decision is kept.
func (s *Service) WithdrawManual(ctx context.Context, request, job string, finding int64) error {
	if !s.ok() || !validRequest(request) || job == "" || finding <= 0 {
		return ErrInvalid
	}
	err := s.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		// the decisions in force on this finding, before it disappears from view
		rows, err := t.Query(`SELECT d.id FROM review_decision d JOIN review_member m ON m.decision=d.id
			WHERE m.finding=? AND m.job=? AND d.state='committed' AND d.revokes IS NULL
			AND NOT EXISTS (SELECT 1 FROM review_decision r WHERE r.revokes=d.id AND r.state='committed') ORDER BY d.seq`, finding, job)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if _, err = s.Findings.WithdrawManual(tx, job, finding); err != nil {
			return err
		}
		for i, id := range ids {
			if err = revokeIn(tx, fmt.Sprintf("%s:%d", request, i), id); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, findings.ErrInvalid), errors.Is(err, ErrInvalid):
		return ErrInvalid
	}
	return wrap(err)
}

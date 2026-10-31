package findings

import (
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// AddManual records a finding the user selected or boxed (FR-22). It is one
// finding in its own sealed attempt, so it raises the result version at once and
// is idempotent per request id: a retried request returns the same finding.
// The caller has already validated the position against the unit's text version.
func (r *Repository) AddManual(tx store.Tx, job, batch, request string, n New) (id int64, version uint64, err error) {
	defer clear(n.Value)
	if job == "" || batch == "" || request == "" || len(request) > 128 || n.Source != kernel.SourceManual {
		return 0, 0, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return 0, 0, err
	}
	attempt := "manual_" + request
	var owner string
	if t.QueryRow("SELECT job FROM findings_attempt WHERE attempt=?", attempt).Scan(&owner) == nil {
		if owner != job {
			return 0, 0, ErrMismatch
		}
		if err = t.QueryRow("SELECT f.id, a.version FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt WHERE f.attempt=?",
			attempt).Scan(&id, &version); err != nil {
			return 0, 0, err
		}
		return id, version, nil
	}
	ref := kernel.AttemptRef{JobID: job, AttemptID: attempt}
	if _, err = r.Add(tx, ref, batch, StageManual, []New{n}); err != nil {
		return 0, 0, err
	}
	if version, err = r.Seal(tx, ref, batch, StageManual); err != nil {
		return 0, 0, err
	}
	if err = t.QueryRow("SELECT id FROM findings_finding WHERE attempt=?", attempt).Scan(&id); err != nil {
		return 0, 0, err
	}
	return id, version, nil
}

// ByIDs returns the visible findings of a job with the given ids, in id order.
// Ids of another job, of an unsealed attempt or that do not exist are absent
// from the result; the caller compares counts.
func (r *Repository) ByIDs(tx store.Tx, job string, ids []int64) ([]Finding, error) {
	if job == "" || len(ids) == 0 || len(ids) > MaxPage {
		return nil, ErrPageLimit
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	args := []any{job}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := t.Query(`SELECT f.id, a.job, f.attempt, a.stage, a.version, f.unit, f.text_version, f.start, f.stop, f.category, f.source, f.score,
		f.low_conf, f.mismatch, f.rule_id, f.field, f.template, f.hmac
		FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt
		WHERE a.sealed=1 AND a.job=? AND f.id IN (?`+repeat(len(ids)-1)+`) ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

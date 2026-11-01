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
		WHERE a.sealed=1 AND a.withdrawn=0 AND a.job=? AND f.id IN (?`+repeat(len(ids)-1)+`) ORDER BY f.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

// WithdrawManual takes back a manual finding of job (FR-22 undo): it is no longer visible to any reading
// (review, resolution, plan, counts), and the job's result version rises, so what was built on the finding
// is built again. Only a visible manual finding can be withdrawn; anything else is ErrInvalid.
func (r *Repository) WithdrawManual(tx store.Tx, job string, id int64) (uint64, error) {
	if job == "" || id <= 0 {
		return 0, ErrInvalid
	}
	t, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var attempt, owner, stage string
	var sealed, withdrawn int
	if err = t.QueryRow(`SELECT a.attempt, a.job, a.stage, a.sealed, a.withdrawn FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt
		WHERE f.id=?`, id).Scan(&attempt, &owner, &stage, &sealed, &withdrawn); err != nil {
		return 0, ErrInvalid
	}
	if owner != job || stage != StageManual || sealed == 0 || withdrawn != 0 {
		return 0, ErrInvalid
	}
	current := r.version(t, job) + 1
	if _, err = sc.Exec(`INSERT INTO findings_result(job, version) VALUES(?,?) ON CONFLICT(job) DO UPDATE SET version=excluded.version`, job, current); err != nil {
		return 0, err
	}
	if _, err = sc.Exec("UPDATE findings_attempt SET withdrawn=1 WHERE attempt=?", attempt); err != nil {
		return 0, err
	}
	tx.Emit(store.Event{Type: "FindingsChanged", Subject: kernel.ArtifactRef{Kind: "findings", ID: job, Version: current}})
	return current, nil
}

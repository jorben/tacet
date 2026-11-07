package review

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

var errNoRows = sql.ErrNoRows

var (
	ErrInvalid      = errors.New("REVIEW_INVALID")
	ErrScopeChanged = errors.New("SCOPE_CHANGED")
	ErrNotFound     = errors.New("REVIEW_DECISION_NOT_FOUND")
	ErrStore        = errors.New("REVIEW_STORE_FAILED")
)

// Module owns the decision tables. Members are references and position facts of
// findings, never text or values.
func Module() store.Module {
	return store.Module{Name: "review", Prefix: "review_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE review_decision(id TEXT PRIMARY KEY, request TEXT NOT NULL UNIQUE, batch TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('item','bulk')), keep INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('pending','committed')),
			seq INTEGER, revokes TEXT, members INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX review_decision_revokes ON review_decision(revokes)`,
		`CREATE TABLE review_member(decision TEXT NOT NULL REFERENCES review_decision(id), finding INTEGER NOT NULL, job TEXT NOT NULL,
			unit TEXT NOT NULL, text_version INTEGER NOT NULL, start INTEGER NOT NULL, stop INTEGER NOT NULL, category TEXT NOT NULL,
			result INTEGER NOT NULL, PRIMARY KEY(decision, finding))`,
		`CREATE INDEX review_member_target ON review_member(job, unit, start, stop)`,
		`CREATE TABLE review_version(job TEXT PRIMARY KEY, version INTEGER NOT NULL)`,
		`CREATE TABLE review_position(key TEXT PRIMARY KEY, filter TEXT NOT NULL, grp TEXT NOT NULL, cursor TEXT NOT NULL)`,
	}}, {Version: 2, SQL: []string{
		`CREATE TABLE review_terms(batch TEXT NOT NULL, version INTEGER NOT NULL, request TEXT NOT NULL UNIQUE, blob BLOB NOT NULL, count INTEGER NOT NULL,
			PRIMARY KEY(batch, version))`,
	}}, {Version: 3, SQL: []string{
		// Every decision asks for the next sequence number, inside a commit that holds the workspace lock for a whole
		// file's list: without this, that query reads the table and the commit grows with the square of the list.
		`CREATE INDEX review_decision_seq ON review_decision(seq)`,
	}}}}
}

// Commit is workspace.Commit; review may import neither workspace nor jobs.
type Commit func(ctx context.Context, fn func(store.Tx) error) error

// DefaultChunk is how many members one write commit carries.
const DefaultChunk = 200

type Service struct {
	// Terms encrypts a batch's supplement terms (the batch key, keys.Batch). Without
	// it the term functions refuse to run: terms are user text and are never stored plain.
	Terms    TermCipher
	Commit   Commit
	Findings *findings.Repository
	Chunk    int // members per commit; 0 means DefaultChunk (at most findings.MaxPage)
}

func (s *Service) chunk() int {
	if s.Chunk <= 0 || s.Chunk > findings.MaxPage {
		return min(DefaultChunk, findings.MaxPage)
	}
	return s.Chunk
}

func (s *Service) ok() bool { return s != nil && s.Commit != nil && s.Findings != nil }

// In is this service with its commits joined to tx: what it writes stands or falls with the rest of the caller's
// commit. Only for the calls that write in one commit; DecideBulk works outside it (MD-11).
func (s *Service) In(tx store.Tx) *Service {
	joined := *s
	joined.Commit = func(_ context.Context, write func(store.Tx) error) error { return write(tx) }
	return &joined
}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := t.Module("review")
	return t, sc, err
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", ErrStore
	}
	return "dec_" + hex.EncodeToString(b), nil
}

func validRequest(r string) bool { return r != "" && len(r) <= 128 }

// Version is the job's decision version: it rises on every committed decision
// or revocation that touches the job, and the disposition stage commits only
// against the version it read (module design 7.1).
func Version(tx store.Tx, job string) (uint64, error) {
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var v uint64
	switch err = t.QueryRow("SELECT version FROM review_version WHERE job=?", job).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return v, nil
}

func bump(tx store.Tx, t *store.Txn, sc *store.Scope, jobs []string) error {
	for _, job := range jobs {
		if _, err := sc.Exec(`INSERT INTO review_version(job, version) VALUES(?,1) ON CONFLICT(job) DO UPDATE SET version=version+1`, job); err != nil {
			return err
		}
		var v uint64
		if err := t.QueryRow("SELECT version FROM review_version WHERE job=?", job).Scan(&v); err != nil {
			return err
		}
		tx.Emit(store.Event{Type: "DecisionsChanged", Subject: kernel.ArtifactRef{Kind: "decisions", ID: job, Version: v}})
	}
	return nil
}

const nextSeqQuery = "SELECT MAX(seq) FROM review_decision"

func nextSeq(t *store.Txn) (int64, error) {
	var n sql.NullInt64
	if err := t.QueryRow(nextSeqQuery).Scan(&n); err != nil {
		return 0, err
	}
	return n.Int64 + 1, nil
}

func jobsOf(t *store.Txn, decision string) ([]string, error) {
	rows, err := t.Query("SELECT DISTINCT job FROM review_member WHERE decision=? ORDER BY job", decision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err = rows.Scan(&j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func insertMember(t *store.Txn, sc *store.Scope, decision string, f findings.Finding, result uint64) error {
	_, err := sc.Exec(`INSERT INTO review_member(decision, finding, job, unit, text_version, start, stop, category, result) VALUES(?,?,?,?,?,?,?,?,?)`,
		decision, f.ID, f.Job, f.Unit, f.TextVersion, f.Start, f.End, f.Category, result)
	return err
}

// ItemRequest is one decision on one finding.
type ItemRequest struct {
	Request string // idempotency key: a retried request returns the same decision
	Batch   string
	Job     string
	Finding int64
	Keep    bool // false: redact
}

// DecideItem commits a single human decision (FR-14, highest priority).
func (s *Service) DecideItem(ctx context.Context, r ItemRequest) (string, error) {
	if !s.ok() || !validRequest(r.Request) || r.Batch == "" || r.Job == "" || r.Finding < 1 {
		return "", ErrInvalid
	}
	var id string
	err := s.Commit(ctx, func(tx store.Tx) error {
		var err error
		id, err = s.decideItem(tx, r)
		return err
	})
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		return "", err
	}
	if err != nil {
		return "", ErrStore
	}
	return id, nil
}

func (s *Service) decideItem(tx store.Tx, r ItemRequest) (string, error) {
	t, sc, err := scope(tx)
	if err != nil {
		return "", err
	}
	var existing string
	if t.QueryRow("SELECT id FROM review_decision WHERE request=?", r.Request).Scan(&existing) == nil {
		return existing, nil
	}
	got, err := s.Findings.ByIDs(tx, r.Job, []int64{r.Finding})
	if err != nil {
		return "", err
	}
	if len(got) != 1 {
		return "", ErrNotFound
	}
	result, err := s.Findings.ResultVersion(tx, r.Job)
	if err != nil {
		return "", err
	}
	seq, err := nextSeq(t)
	if err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if _, err = sc.Exec(`INSERT INTO review_decision(id, request, batch, kind, keep, state, seq, members) VALUES(?,?,?,'item',?,'committed',?,1)`,
		id, r.Request, r.Batch, boolInt(r.Keep), seq); err != nil {
		return "", err
	}
	if err = insertMember(t, sc, id, got[0], result); err != nil {
		return "", err
	}
	return id, bump(tx, t, sc, []string{r.Job})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Revoke records that a decision no longer applies. History is kept: the
// revocation is a new record that names the original and copies no members
// (PF-14). Revoking twice is a no-op, so a retried undo changes nothing.
func (s *Service) Revoke(ctx context.Context, request, decision string) error {
	if !s.ok() || !validRequest(request) || decision == "" {
		return ErrInvalid
	}
	err := s.Commit(ctx, func(tx store.Tx) error { return revokeIn(tx, request, decision) })
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil {
		return ErrStore
	}
	return nil
}

// DropPending removes decisions that never reached "committed", after a crash
// or an abandoned bulk request (MD-11). Readers ignore them meanwhile.
func (s *Service) DropPending(ctx context.Context) (int, error) {
	if !s.ok() {
		return 0, ErrInvalid
	}
	n := 0
	err := s.Commit(ctx, func(tx store.Tx) error {
		t, sc, err := scope(tx)
		if err != nil {
			return err
		}
		if _, err = sc.Exec("DELETE FROM review_member WHERE decision IN (SELECT id FROM review_decision WHERE state='pending')"); err != nil {
			return err
		}
		return t.QueryRow("SELECT COUNT(*) FROM review_decision WHERE state='pending'").Scan(&n)
	})
	if err != nil {
		return 0, ErrStore
	}
	err = s.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec("DELETE FROM review_decision WHERE state='pending'")
		return err
	})
	if err != nil {
		return 0, ErrStore
	}
	return n, nil
}

// Purge deletes everything review holds for a job (retention). A bulk decision
// that spans several jobs loses only this job's members.
func Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM review_member WHERE job=?",
		"DELETE FROM review_version WHERE job=?",
	} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

// revokeIn is Revoke inside the caller's commit.
func revokeIn(tx store.Tx, request, decision string) error {
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	var kind, state string
	var keep int
	var revokes sql.NullString
	if err = t.QueryRow("SELECT kind, keep, state, revokes FROM review_decision WHERE id=?", decision).Scan(&kind, &keep, &state, &revokes); err != nil {
		return ErrNotFound
	}
	if state != "committed" || revokes.Valid {
		return ErrInvalid // pending decisions are dropped, revocations are not revoked
	}
	var n int
	if err = t.QueryRow("SELECT COUNT(*) FROM review_decision WHERE request=? OR (revokes=? AND state='committed')", request, decision).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	seq, err := nextSeq(t)
	if err != nil {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	if _, err = sc.Exec(`INSERT INTO review_decision(id, request, batch, kind, keep, state, seq, revokes, members)
		SELECT ?, ?, batch, kind, keep, 'committed', ?, id, 0 FROM review_decision WHERE id=?`, id, request, seq, decision); err != nil {
		return err
	}
	jobs, err := jobsOf(t, decision)
	if err != nil {
		return err
	}
	return bump(tx, t, sc, jobs)
}

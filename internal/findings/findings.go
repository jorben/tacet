package findings

import (
	"encoding/hex"
	"errors"
	"math"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrInvalid   = errors.New("FINDING_INVALID")
	ErrSealed    = errors.New("FINDING_ATTEMPT_SEALED")
	ErrMismatch  = errors.New("FINDING_ATTEMPT_MISMATCH")
	ErrPageLimit = errors.New("FINDING_PAGE_LIMIT")
)

const MaxPage = 200

// Stages that add findings (module design 6.5).
const (
	StageDetect     = "detect"
	StageSupplement = "supplement"
	StagePostExport = "post_export"
	StageManual     = "manual"
)

var stages = map[string]bool{StageDetect: true, StageSupplement: true, StagePostExport: true, StageManual: true}
var sources = map[kernel.FindingSource]bool{kernel.SourceRule: true, kernel.SourceDictionary: true, kernel.SourceModel: true,
	kernel.SourceFieldRule: true, kernel.SourceManual: true, kernel.SourceUserTerm: true}

// Module owns the finding tables. No column holds an original value or an
// unkeyed hash of one.
func Module() store.Module {
	return store.Module{Name: "findings", Prefix: "findings_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE findings_attempt(attempt TEXT PRIMARY KEY, job TEXT NOT NULL, batch TEXT NOT NULL, stage TEXT NOT NULL,
			sealed INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE findings_result(job TEXT PRIMARY KEY, version INTEGER NOT NULL)`,
		`CREATE TABLE findings_finding(id INTEGER PRIMARY KEY AUTOINCREMENT, attempt TEXT NOT NULL REFERENCES findings_attempt(attempt),
			unit TEXT NOT NULL, text_version INTEGER NOT NULL, start INTEGER NOT NULL, stop INTEGER NOT NULL,
			category TEXT NOT NULL, source TEXT NOT NULL, score REAL NOT NULL, low_conf INTEGER NOT NULL, mismatch INTEGER NOT NULL,
			rule_id TEXT NOT NULL, field TEXT NOT NULL, template TEXT NOT NULL, hmac BLOB)`,
		`CREATE INDEX findings_by_attempt ON findings_finding(attempt, id)`,
		`CREATE INDEX findings_by_group ON findings_finding(category, source, field, template, hmac)`,
	}}}}
}

// New is one finding to add. Value is the matched text, used only to compute the
// same-value HMAC and cleared before Add returns; nil for findings that need no
// grouping by value.
type New struct {
	Unit        string
	TextVersion uint64
	Start, End  int
	Category    string
	Source      kernel.FindingSource
	Score       float64
	LowConf     bool
	Mismatch    bool
	RuleID      string
	Field       string
	Template    string
	Value       []byte
}

// Finding is a stored finding.
type Finding struct {
	ID          int64
	Job         string
	Attempt     string
	Stage       string
	Version     uint64
	Unit        string
	TextVersion uint64
	Start, End  int
	Category    string
	Source      kernel.FindingSource
	Score       float64
	LowConf     bool
	Mismatch    bool
	RuleID      string
	Field       string
	Template    string
	Group       string // hex of the same-value HMAC; "" when none
}

// BatchKey returns the key of a batch (keys.Manager.Get with keys.Batch).
type BatchKey func(batch string) ([]byte, error)

type Repository struct{ key BatchKey }

func NewRepository(key BatchKey) (*Repository, error) {
	if key == nil {
		return nil, ErrInvalid
	}
	return &Repository{key: key}, nil
}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("findings")
	return t, s, err
}

func (n New) valid() bool {
	return n.Unit != "" && n.Start >= 0 && n.Start < n.End && n.Category != "" && sources[n.Source] &&
		!math.IsNaN(n.Score) && !math.IsInf(n.Score, 0) && n.Score >= 0 && n.Score <= 1
}

// Add stores findings of a stage attempt. They stay invisible to queries until
// Seal, so a stage that fails or is abandoned half-way leaves nothing that could
// be mistaken for a complete result. An item that is invalid rejects the whole
// call and nothing is stored.
func (r *Repository) Add(tx store.Tx, a kernel.AttemptRef, batch, stage string, items []New) (int, error) {
	defer func() {
		for _, it := range items {
			clear(it.Value)
		}
	}()
	if a.JobID == "" || a.AttemptID == "" || batch == "" || !stages[stage] {
		return 0, ErrInvalid
	}
	for _, it := range items {
		if !it.valid() {
			return 0, ErrInvalid
		}
	}
	t, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	if sealed, err := ensure(t, sc, a, batch, stage); err != nil {
		return 0, err
	} else if sealed {
		return 0, ErrSealed
	}
	var batchKey []byte
	defer func() { keys.Zero(batchKey) }()
	for _, it := range items {
		var mac any
		if len(it.Value) > 0 {
			if batchKey == nil {
				if batchKey, err = r.key(batch); err != nil {
					return 0, err
				}
			}
			sum, err := keys.GroupHMAC(batchKey, it.Value)
			if err != nil {
				return 0, err
			}
			mac = sum
		}
		if _, err = sc.Exec(`INSERT INTO findings_finding(attempt, unit, text_version, start, stop, category, source, score, low_conf, mismatch, rule_id, field, template, hmac)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.AttemptID, it.Unit, it.TextVersion, it.Start, it.End, it.Category, string(it.Source), it.Score,
			it.LowConf, it.Mismatch, it.RuleID, it.Field, it.Template, mac); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// ensure registers the attempt on first use and checks that later calls name the
// same job, batch and stage.
func ensure(t *store.Txn, sc *store.Scope, a kernel.AttemptRef, batch, stage string) (sealed bool, err error) {
	var job, b, st string
	var flag int
	switch err = t.QueryRow("SELECT job, batch, stage, sealed FROM findings_attempt WHERE attempt=?", a.AttemptID).Scan(&job, &b, &st, &flag); {
	case err == nil:
		if job != a.JobID || b != batch || st != stage {
			return false, ErrMismatch
		}
		return flag != 0, nil
	default:
		_, err = sc.Exec("INSERT INTO findings_attempt(attempt, job, batch, stage) VALUES(?,?,?,?)", a.AttemptID, a.JobID, batch, stage)
		return false, err
	}
}

// Seal makes an attempt's findings visible and returns the job's result
// version. The version rises only when the attempt added at least one finding:
// a stage that found nothing new does not invalidate decisions made on the
// previous version.
func (r *Repository) Seal(tx store.Tx, a kernel.AttemptRef, batch, stage string) (uint64, error) {
	if a.JobID == "" || a.AttemptID == "" || batch == "" || !stages[stage] {
		return 0, ErrInvalid
	}
	t, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	sealed, err := ensure(t, sc, a, batch, stage) // a stage with no findings seals too
	if err != nil {
		return 0, err
	}
	job := a.JobID
	if sealed {
		var version uint64
		if err = t.QueryRow("SELECT version FROM findings_attempt WHERE attempt=?", a.AttemptID).Scan(&version); err != nil {
			return 0, err
		}
		return version, nil // sealing is idempotent: a replayed completion changes nothing
	}
	var n int
	if err = t.QueryRow("SELECT COUNT(*) FROM findings_finding WHERE attempt=?", a.AttemptID).Scan(&n); err != nil {
		return 0, err
	}
	current := r.version(t, job)
	if n > 0 {
		current++
		if _, err = sc.Exec(`INSERT INTO findings_result(job, version) VALUES(?,?) ON CONFLICT(job) DO UPDATE SET version=excluded.version`, job, current); err != nil {
			return 0, err
		}
		tx.Emit(store.Event{Type: "FindingsChanged", Subject: kernel.ArtifactRef{Kind: "findings", ID: job, Version: current}})
	}
	if _, err = sc.Exec("UPDATE findings_attempt SET sealed=1, version=? WHERE attempt=?", current, a.AttemptID); err != nil {
		return 0, err
	}
	return current, nil
}

func (r *Repository) version(t *store.Txn, job string) uint64 {
	var v uint64
	if t.QueryRow("SELECT version FROM findings_result WHERE job=?", job).Scan(&v) != nil {
		return 0
	}
	return v
}

// ResultVersion is the job's current detection result version (0 before any).
func (r *Repository) ResultVersion(tx store.Tx, job string) (uint64, error) {
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	return r.version(t, job), nil
}

// Discard removes the findings of an attempt that did not complete (recovery,
// cancellation, a late result). A sealed attempt is part of a result and stays.
func (r *Repository) Discard(tx store.Tx, attempt string) error {
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	var sealed int
	switch err = t.QueryRow("SELECT sealed FROM findings_attempt WHERE attempt=?", attempt).Scan(&sealed); {
	case err != nil:
		return nil // nothing was added
	case sealed != 0:
		return ErrSealed
	}
	if _, err = sc.Exec("DELETE FROM findings_finding WHERE attempt=?", attempt); err != nil {
		return err
	}
	_, err = sc.Exec("DELETE FROM findings_attempt WHERE attempt=?", attempt)
	return err
}

// Purge removes everything of a job (retention).
func (r *Repository) Purge(tx store.Tx, job string) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM findings_finding WHERE attempt IN (SELECT attempt FROM findings_attempt WHERE job=?)",
		"DELETE FROM findings_attempt WHERE job=?", "DELETE FROM findings_result WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

// Filter narrows a query. Zero values mean "any". MinScore/MaxScore apply to
// scored findings (the model's); a range is how candidates below the minimum
// confidence are inspected (MD-12).
type Filter struct {
	Job        string
	Batch      string
	Categories []string
	Sources    []kernel.FindingSource
	MinScore   *float64
	MaxScore   *float64
	Field      string
	Template   string
	Group      string
	Units      []string // restrict to these text units (a viewport)
}

func (f Filter) where() (string, []any, error) {
	if f.Job == "" && f.Batch == "" {
		return "", nil, ErrInvalid
	}
	w := " WHERE a.sealed=1"
	var args []any
	if f.Job != "" {
		w += " AND a.job=?"
		args = append(args, f.Job)
	}
	if f.Batch != "" {
		w += " AND a.batch=?"
		args = append(args, f.Batch)
	}
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		w += " AND " + col + " IN (?" + repeat(len(vals)-1) + ")"
		for _, v := range vals {
			args = append(args, v)
		}
	}
	// Unary plus keeps the planner off the category/source indexes: they cannot give
	// id order, and sorting every match for every page makes paging quadratic.
	in("+f.category", f.Categories)
	in("f.unit", f.Units)
	srcs := make([]string, len(f.Sources))
	for i, s := range f.Sources {
		srcs[i] = string(s)
	}
	in("+f.source", srcs)
	if f.MinScore != nil {
		w += " AND f.score>=?"
		args = append(args, *f.MinScore)
	}
	if f.MaxScore != nil {
		w += " AND f.score<=?"
		args = append(args, *f.MaxScore)
	}
	if f.Field != "" {
		w += " AND f.field=?"
		args = append(args, f.Field)
	}
	if f.Template != "" {
		w += " AND f.template=?"
		args = append(args, f.Template)
	}
	if f.Group != "" {
		raw, err := hex.DecodeString(f.Group)
		if err != nil {
			return "", nil, ErrInvalid
		}
		w += " AND f.hmac=?"
		args = append(args, raw)
	}
	return w, args, nil
}

func repeat(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += ",?"
	}
	return s
}

// Page returns visible findings in id order after the given id (keyset paging,
// so a refresh never skips or repeats rows).
// pageSQL is the keyset page query for a WHERE clause from Filter.where.
func pageSQL(where string) string {
	return `SELECT f.id, a.job, f.attempt, a.stage, a.version, f.unit, f.text_version, f.start, f.stop, f.category, f.source, f.score,
		f.low_conf, f.mismatch, f.rule_id, f.field, f.template, f.hmac
		FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt` + where + ` AND f.id>? ORDER BY f.id LIMIT ?`
}

func (r *Repository) Page(tx store.Tx, f Filter, afterID int64, limit int) ([]Finding, error) {
	if limit <= 0 || limit > MaxPage || afterID < 0 {
		return nil, ErrPageLimit
	}
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(pageSQL(where), append(args, afterID, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanFindings(rows rowScanner) ([]Finding, error) {
	var out []Finding
	for rows.Next() {
		var x Finding
		var src string
		var mac []byte
		if err := rows.Scan(&x.ID, &x.Job, &x.Attempt, &x.Stage, &x.Version, &x.Unit, &x.TextVersion, &x.Start, &x.End, &x.Category, &src,
			&x.Score, &x.LowConf, &x.Mismatch, &x.RuleID, &x.Field, &x.Template, &mac); err != nil {
			return nil, err
		}
		x.Source = kernel.FindingSource(src)
		if len(mac) > 0 {
			x.Group = hex.EncodeToString(mac)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Count is the number of visible findings the filter matches. It uses the same
// predicate as Page, so a count shown to the user and the members later
// materialised from the same filter cannot disagree (MD-11).
func (r *Repository) Count(tx store.Tx, f Filter) (int, error) {
	where, args, err := f.where()
	if err != nil {
		return 0, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var n int
	err = t.QueryRow(`SELECT COUNT(*) FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt`+where, args...).Scan(&n)
	return n, err
}

// CountByCategory counts the visible findings of f per category.
func (r *Repository) CountByCategory(tx store.Tx, f Filter) (map[string]int, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT f.category, COUNT(*) FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt`+where+` GROUP BY f.category`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		if err = rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// Group is one aggregate of identical (category, source, field, template, value).
type Group struct {
	Category       string
	Source         kernel.FindingSource
	Field          string
	Template       string
	Value          string // hex HMAC, "" when findings carry none
	Findings       int
	Jobs           int
	Representative int64 // lowest finding id of the group: stable across refreshes
}

// GroupKey is the keyset position in group order.
type GroupKey struct{ Category, Source, Field, Template, Value string }

// Groups aggregates visible findings. Groups come in a fixed key order and are
// paged by the last key, so the list does not jump between refreshes (BUX-05).
func (r *Repository) Groups(tx store.Tx, f Filter, after GroupKey, limit int) ([]Group, error) {
	if limit <= 0 || limit > MaxPage {
		return nil, ErrPageLimit
	}
	where, args, err := f.where()
	if err != nil {
		return nil, err
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	having := ""
	if after != (GroupKey{}) {
		having = " HAVING (f.category, f.source, f.field, f.template, hex(coalesce(f.hmac, x''))) > (?,?,?,?,?)"
		args = append(args, after.Category, after.Source, after.Field, after.Template, after.Value)
	}
	rows, err := t.Query(`SELECT f.category, f.source, f.field, f.template, hex(coalesce(f.hmac, x'')), COUNT(*), COUNT(DISTINCT a.job), MIN(f.id)
		FROM findings_finding f JOIN findings_attempt a ON a.attempt=f.attempt`+where+
		` GROUP BY f.category, f.source, f.field, f.template, hex(coalesce(f.hmac, x''))`+having+
		` ORDER BY f.category, f.source, f.field, f.template, hex(coalesce(f.hmac, x'')) LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var src string
		if err = rows.Scan(&g.Category, &src, &g.Field, &g.Template, &g.Value, &g.Findings, &g.Jobs, &g.Representative); err != nil {
			return nil, err
		}
		g.Source = kernel.FindingSource(src)
		out = append(out, g)
	}
	return out, rows.Err()
}

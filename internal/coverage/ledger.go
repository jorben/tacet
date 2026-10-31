package coverage

import (
	"errors"

	"github.com/jorben/tacet/internal/store"
)

var (
	ErrStaleAttempt = errors.New("COVERAGE_ATTEMPT_STALE")
	ErrLedger       = errors.New("COVERAGE_LEDGER_INVALID")
	ErrNoInventory  = errors.New("COVERAGE_INVENTORY_MISSING")
)

// MaxParts bounds one file's ledger (protocol: at most 16384 parts).
const MaxParts = 16384

// Module owns the persisted ledger. Only part metadata is stored, never text.
func Module() store.Module {
	return store.Module{Name: "coverage", Prefix: "coverage_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE coverage_inventory(job TEXT PRIMARY KEY, attempt TEXT NOT NULL, complete INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE coverage_part(job TEXT NOT NULL REFERENCES coverage_inventory(job), part TEXT NOT NULL, class TEXT NOT NULL,
			required INTEGER NOT NULL, known INTEGER NOT NULL, bounded INTEGER NOT NULL,
			inspection TEXT NOT NULL, disposition TEXT NOT NULL, validation TEXT NOT NULL, PRIMARY KEY(job, part))`,
	}}}}
}

type Repository struct{}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("coverage")
	return t, s, err
}

// Begin starts the ledger of an extraction attempt: the previous attempt's rows
// are dropped (its results are being redone) and the inventory is marked
// incomplete until Complete. Beginning the same attempt twice is refused so a
// replayed start cannot wipe rows already recorded.
func (Repository) Begin(tx store.Tx, job, attempt string) error {
	if job == "" || attempt == "" {
		return ErrLedger
	}
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	var current string
	if t.QueryRow("SELECT attempt FROM coverage_inventory WHERE job=?", job).Scan(&current) == nil && current == attempt {
		return ErrStaleAttempt
	}
	if _, err = sc.Exec("DELETE FROM coverage_part WHERE job=?", job); err != nil {
		return err
	}
	_, err = sc.Exec(`INSERT INTO coverage_inventory(job, attempt, complete) VALUES(?,?,0)
		ON CONFLICT(job) DO UPDATE SET attempt=excluded.attempt, complete=0`, job, attempt)
	return err
}

func (Repository) owns(t *store.Txn, job, attempt string) (complete bool, err error) {
	var current string
	var flag int
	if t.QueryRow("SELECT attempt, complete FROM coverage_inventory WHERE job=?", job).Scan(&current, &flag) != nil {
		return false, ErrNoInventory
	}
	if current != attempt {
		return false, ErrStaleAttempt
	}
	return flag != 0, nil
}

func valid(p Part) bool {
	switch p.Inspection {
	case Inspected, NotInspected, Unsupported, InspectFail:
	default:
		return false
	}
	switch p.Disposition {
	case Redacted, Removed, RetainedByPolicy, Pending:
	default:
		return false
	}
	switch p.Validation {
	case PassedWithinScope, PartialPass, ValidationFailed, NotRun:
	default:
		return false
	}
	switch p.Class {
	case ClassText, ClassScanPage, ClassImage, ClassEmbedded, ClassOutlined, ClassOther:
	default:
		return false
	}
	return p.ID != "" && len(p.ID) <= 1024
}

// Record stores one part of the attempt's ledger, replacing an earlier row for
// the same part. A completed ledger is closed.
func (r Repository) Record(tx store.Tx, job, attempt string, p Part) error {
	if !valid(p) {
		return ErrLedger
	}
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	done, err := r.owns(t, job, attempt)
	if err != nil {
		return err
	}
	if done {
		return ErrLedger
	}
	var n int
	if err = t.QueryRow("SELECT COUNT(*) FROM coverage_part WHERE job=? AND part<>?", job, p.ID).Scan(&n); err != nil {
		return err
	}
	if n >= MaxParts {
		return ErrLedger
	}
	_, err = sc.Exec(`INSERT INTO coverage_part(job, part, class, required, known, bounded, inspection, disposition, validation)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(job, part) DO UPDATE SET class=excluded.class, required=excluded.required, known=excluded.known,
		bounded=excluded.bounded, inspection=excluded.inspection, disposition=excluded.disposition, validation=excluded.validation`,
		job, p.ID, string(p.Class), p.Required, p.Known, p.Bounded, string(p.Inspection), string(p.Disposition), string(p.Validation))
	return err
}

// Complete closes the ledger: the enumeration of parts is finished. Only then
// does Ledger report the inventory as complete.
func (r Repository) Complete(tx store.Tx, job, attempt string) error {
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	if _, err = r.owns(t, job, attempt); err != nil {
		return err
	}
	_, err = sc.Exec("UPDATE coverage_inventory SET complete=1 WHERE job=? AND attempt=?", job, attempt)
	return err
}

// Ledger returns the parts in id order and whether the enumeration finished.
// Feed both to Assess: an incomplete inventory is an error there, not a list.
func (Repository) Ledger(tx store.Tx, job string) ([]Part, bool, error) {
	t, _, err := scope(tx)
	if err != nil {
		return nil, false, err
	}
	var flag int
	if t.QueryRow("SELECT complete FROM coverage_inventory WHERE job=?", job).Scan(&flag) != nil {
		return nil, false, ErrNoInventory
	}
	rows, err := t.Query(`SELECT part, class, required, known, bounded, inspection, disposition, validation FROM coverage_part WHERE job=? ORDER BY part`, job)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Part
	for rows.Next() {
		var p Part
		var class, ins, disp, val string
		if err = rows.Scan(&p.ID, &class, &p.Required, &p.Known, &p.Bounded, &ins, &disp, &val); err != nil {
			return nil, false, err
		}
		p.Class, p.Inspection, p.Disposition, p.Validation = ContentClass(class), Inspection(ins), Disposition(disp), Validation(val)
		out = append(out, p)
	}
	return out, flag != 0, rows.Err()
}

// Purge removes a job's ledger (retention).
func (Repository) Purge(tx store.Tx, job string) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	if _, err = sc.Exec("DELETE FROM coverage_part WHERE job=?", job); err != nil {
		return err
	}
	_, err = sc.Exec("DELETE FROM coverage_inventory WHERE job=?", job)
	return err
}

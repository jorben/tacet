package extract

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jorben/tacet/internal/store"
)

var (
	ErrUnits  = errors.New("EXTRACT_UNIT_INVALID")
	ErrSealed = errors.New("EXTRACT_ATTEMPT_SEALED")
	ErrBody   = errors.New("EXTRACT_BODY_UNAVAILABLE")
)

// Module owns the text-unit metadata. Bodies live in encrypted resource files;
// the database holds references, positions and counts, never text.
func Module() store.Module {
	return store.Module{Name: "extract", Prefix: "extract_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE extract_attempt(attempt TEXT PRIMARY KEY, job TEXT NOT NULL, sealed INTEGER NOT NULL DEFAULT 0, units INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE extract_unit(attempt TEXT NOT NULL REFERENCES extract_attempt(attempt), seq INTEGER NOT NULL, unit_id TEXT NOT NULL,
			part TEXT NOT NULL, ordinal INTEGER NOT NULL, visibility TEXT NOT NULL, chars INTEGER NOT NULL, body TEXT NOT NULL,
			PRIMARY KEY(attempt, seq))`,
	}}}}
}

// Unit is the stored metadata of one text unit.
type Unit struct {
	Attempt    string
	Seq        int
	UnitID     string
	Part       string
	Ordinal    int
	Visibility string
	Chars      int
	Body       string // resource id of the body
}

type Repository struct{}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("extract")
	return t, s, err
}

// Add records the next unit of an open attempt. Seq must follow the previous
// one, so a replayed or skipped unit is a protocol error, not a quiet gap.
func (Repository) Add(tx store.Tx, job string, u Unit) error {
	if job == "" || u.Attempt == "" || u.UnitID == "" || u.Part == "" || u.Ordinal < 1 || u.Chars < 1 || u.Body == "" || u.Seq < 1 {
		return ErrUnits
	}
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	var sealed, have int
	switch err = t.QueryRow("SELECT sealed, units FROM extract_attempt WHERE attempt=?", u.Attempt).Scan(&sealed, &have); {
	case err != nil:
		if u.Seq != 1 {
			return ErrUnits
		}
		if _, err = sc.Exec("INSERT INTO extract_attempt(attempt, job) VALUES(?,?)", u.Attempt, job); err != nil {
			return err
		}
	case sealed != 0:
		return ErrSealed
	case u.Seq != have+1:
		return ErrUnits
	}
	if _, err = sc.Exec(`INSERT INTO extract_unit(attempt, seq, unit_id, part, ordinal, visibility, chars, body) VALUES(?,?,?,?,?,?,?,?)`,
		u.Attempt, u.Seq, u.UnitID, u.Part, u.Ordinal, u.Visibility, u.Chars, u.Body); err != nil {
		return err
	}
	_, err = sc.Exec("UPDATE extract_attempt SET units=? WHERE attempt=?", u.Seq, u.Attempt)
	return err
}

// Seal makes the attempt's units the job's text units. It returns the count and
// refuses a count that differs from what the plugin declared at completion.
func (Repository) Seal(tx store.Tx, job, attempt string, declared int) (int, error) {
	t, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	var owner string
	var sealed, have int
	if err = t.QueryRow("SELECT job, sealed, units FROM extract_attempt WHERE attempt=?", attempt).Scan(&owner, &sealed, &have); err != nil {
		if declared != 0 { // a file without text units has no rows to seal
			return 0, ErrUnits
		}
		if _, err = sc.Exec("INSERT INTO extract_attempt(attempt, job, sealed) VALUES(?,?,1)", attempt, job); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if owner != job || have != declared {
		return 0, ErrUnits
	}
	_, err = sc.Exec("UPDATE extract_attempt SET sealed=1 WHERE attempt=?", attempt)
	return have, err
}

// Discard removes an unfinished attempt and returns its body ids for deletion.
func (Repository) Discard(tx store.Tx, attempt string) ([]string, error) {
	t, sc, err := scope(tx)
	if err != nil {
		return nil, err
	}
	var sealed int
	if t.QueryRow("SELECT sealed FROM extract_attempt WHERE attempt=?", attempt).Scan(&sealed) != nil {
		return nil, nil
	}
	if sealed != 0 {
		return nil, ErrSealed
	}
	rows, err := t.Query("SELECT body FROM extract_unit WHERE attempt=?", attempt)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if _, err = sc.Exec("DELETE FROM extract_unit WHERE attempt=?", attempt); err != nil {
		return nil, err
	}
	_, err = sc.Exec("DELETE FROM extract_attempt WHERE attempt=?", attempt)
	return ids, err
}

// Units pages the sealed units of an attempt in sequence order.
func (Repository) Units(tx store.Tx, attempt string, after, limit int) ([]Unit, error) {
	if limit < 1 || limit > 200 || after < 0 {
		return nil, ErrUnits
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT u.attempt, u.seq, u.unit_id, u.part, u.ordinal, u.visibility, u.chars, u.body FROM extract_unit u
		JOIN extract_attempt a ON a.attempt=u.attempt WHERE a.sealed=1 AND u.attempt=? AND u.seq>? ORDER BY u.seq LIMIT ?`, attempt, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		var u Unit
		if err = rows.Scan(&u.Attempt, &u.Seq, &u.UnitID, &u.Part, &u.Ordinal, &u.Visibility, &u.Chars, &u.Body); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Bodies stores unit text outside the database.
type Bodies interface {
	Put(id, text string) error
	Get(id string) (string, error)
	Delete(id string) error
}

// FileBodies keeps each body as one encrypted resource file (segmented
// AES-256-GCM, 5.1). Key returns the key of a resource id; the caller derives
// it from the task key so crypto-erasing the task makes every body unreadable.
type FileBodies struct {
	Dir string
	Key func(id string) ([]byte, error)
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (b FileBodies) path(id string) (string, error) {
	if !idPattern.MatchString(id) || !filepath.IsAbs(b.Dir) {
		return "", ErrBody
	}
	return filepath.Join(b.Dir, id+".body"), nil
}

func (b FileBodies) Put(id, text string) error {
	path, err := b.path(id)
	if err != nil {
		return err
	}
	key, err := b.Key(id)
	if err != nil {
		return ErrBody
	}
	defer clear(key)
	if err = os.MkdirAll(b.Dir, 0o700); err != nil {
		return ErrBody
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrBody
	}
	w, err := store.NewResourceWriter(f, key, id, uint64(len(text))+1<<20)
	if err == nil {
		_, err = io.WriteString(w, text)
	}
	if err == nil {
		err = w.Close()
	}
	if cerr := f.Close(); err != nil || cerr != nil {
		os.Remove(path)
		return ErrBody
	}
	return nil
}

func (b FileBodies) Get(id string) (string, error) {
	path, err := b.path(id)
	if err != nil {
		return "", err
	}
	key, err := b.Key(id)
	if err != nil {
		return "", ErrBody
	}
	defer clear(key)
	f, err := os.Open(path)
	if err != nil {
		return "", ErrBody
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", ErrBody
	}
	r, err := store.OpenResource(f, info.Size(), key, id)
	if err != nil {
		return "", ErrBody
	}
	defer r.Close()
	data := make([]byte, r.Size())
	if _, err = r.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", ErrBody
	}
	return string(data), nil
}

func (b FileBodies) Delete(id string) error {
	path, err := b.path(id)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrBody
	}
	return nil
}

// Sealed returns the attempt whose units are the job's current text units: the
// latest sealed attempt that has any.
func (Repository) Sealed(tx store.Tx, job string) (string, bool, error) {
	t, _, err := scope(tx)
	if err != nil {
		return "", false, err
	}
	var attempt string
	switch err = t.QueryRow(`SELECT attempt FROM extract_attempt WHERE job=? AND sealed=1 ORDER BY rowid DESC LIMIT 1`, job).Scan(&attempt); {
	case err == nil:
		return attempt, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	}
	return "", false, err
}

// ByID finds one sealed unit of an attempt by the id the plugin gave it.
func (Repository) ByID(tx store.Tx, attempt, unitID string) (Unit, bool, error) {
	t, _, err := scope(tx)
	if err != nil {
		return Unit{}, false, err
	}
	var u Unit
	err = t.QueryRow(`SELECT u.attempt, u.seq, u.unit_id, u.part, u.ordinal, u.visibility, u.chars, u.body FROM extract_unit u
		JOIN extract_attempt a ON a.attempt=u.attempt WHERE a.sealed=1 AND u.attempt=? AND u.unit_id=?`, attempt, unitID).
		Scan(&u.Attempt, &u.Seq, &u.UnitID, &u.Part, &u.Ordinal, &u.Visibility, &u.Chars, &u.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return Unit{}, false, nil
	}
	return u, err == nil, err
}

// ByPosition finds a sealed unit by the source position the plugin reported.
func (Repository) ByPosition(tx store.Tx, attempt, part string, ordinal int) (Unit, bool, error) {
	t, _, err := scope(tx)
	if err != nil {
		return Unit{}, false, err
	}
	var u Unit
	err = t.QueryRow(`SELECT u.attempt, u.seq, u.unit_id, u.part, u.ordinal, u.visibility, u.chars, u.body FROM extract_unit u
		JOIN extract_attempt a ON a.attempt=u.attempt WHERE a.sealed=1 AND u.attempt=? AND u.part=? AND u.ordinal=?`, attempt, part, ordinal).
		Scan(&u.Attempt, &u.Seq, &u.UnitID, &u.Part, &u.Ordinal, &u.Visibility, &u.Chars, &u.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return Unit{}, false, nil
	}
	return u, err == nil, err
}

// ByPart lists the sealed units of one part (a page, say) in order.
func (Repository) ByPart(tx store.Tx, attempt, part string, limit int) ([]Unit, error) {
	if limit < 1 || limit > 10000 {
		return nil, ErrUnits
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT u.attempt, u.seq, u.unit_id, u.part, u.ordinal, u.visibility, u.chars, u.body FROM extract_unit u
		JOIN extract_attempt a ON a.attempt=u.attempt WHERE a.sealed=1 AND u.attempt=? AND u.part=? ORDER BY u.ordinal LIMIT ?`, attempt, part, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		var u Unit
		if err = rows.Scan(&u.Attempt, &u.Seq, &u.UnitID, &u.Part, &u.Ordinal, &u.Visibility, &u.Chars, &u.Body); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

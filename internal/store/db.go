package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jorben/tacet/internal/kernel"
)

var (
	ErrUnknownModule = errors.New("STORE_MODULE_UNKNOWN")
	ErrTxDone        = errors.New("STORE_TX_DONE")
	ErrBadAck        = errors.New("STORE_ACK_INVALID")
)

// DB is a migrated database with registered module namespaces. The *sql.DB is
// opened by the caller, which applies the encryption layer and key (the
// encryption candidate is decided in M0); nothing here depends on it.
type DB struct {
	sql *sql.DB
	reg *Registry
}

// Open migrates the database and returns it ready for use. If migration fails
// or the schema is newer than this build the workspace must not be used.
func Open(ctx context.Context, db *sql.DB, reg *Registry, backup Backup) (*DB, error) {
	if db == nil || reg == nil {
		return nil, ErrMigrationSetup
	}
	// One connection: the workspace serialises writers anyway, and a single
	// connection keeps temp state and pragmas per database, not per pool slot.
	db.SetMaxOpenConns(1)
	if err := reg.Migrate(ctx, db, backup); err != nil {
		return nil, err
	}
	return &DB{sql: db, reg: reg}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// Txn is one transaction. It satisfies Tx and adds Commit/Rollback and
// module-scoped writes.
type Txn struct {
	db      *DB
	tx      *sql.Tx
	ctx     context.Context
	hooks   []func()
	emitErr error
	done    bool
}

var _ Tx = (*Txn)(nil)

// InTransaction unwraps a workspace transaction without opening a nested
// connection. Repositories use the returned scope for their own tables only.
func InTransaction(tx Tx) (*Txn, error) {
	for depth := 0; depth < 8 && tx != nil; depth++ {
		if concrete, ok := tx.(*Txn); ok {
			return concrete, nil
		}
		wrapper, ok := tx.(interface{ Underlying() Tx })
		if !ok {
			break
		}
		tx = wrapper.Underlying()
	}
	return nil, ErrTxDone
}

func (d *DB) Begin(ctx context.Context) (*Txn, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Txn{db: d, tx: tx, ctx: ctx}, nil
}

// AfterCommit registers a hook that runs once, after the data is durable.
func (t *Txn) AfterCommit(fn func()) { t.hooks = append(t.hooks, fn) }

// Emit appends an event to the outbox inside this transaction, so the event
// exists if and only if the change does. Failure is remembered and makes the
// commit fail: an event can never be silently lost.
func (t *Txn) Emit(e Event) {
	if t.done || t.emitErr != nil {
		return
	}
	_, t.emitErr = t.tx.ExecContext(t.ctx, `INSERT INTO store_outbox(type, kind, id, version, digest) VALUES(?,?,?,?,?)`,
		e.Type, string(e.Subject.Kind), e.Subject.ID, int64(e.Subject.Version), e.Subject.Digest)
}

func (t *Txn) Commit() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	if t.emitErr != nil {
		t.tx.Rollback()
		return t.emitErr
	}
	if err := t.tx.Commit(); err != nil {
		return err
	}
	for _, h := range t.hooks {
		h()
	}
	t.hooks = nil
	return nil
}

func (t *Txn) Rollback() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	t.hooks = nil
	return t.tx.Rollback()
}

// Scope is the write handle of one module: it refuses statements that target
// tables outside the module's namespace (MD-01).
type Scope struct {
	t *Txn
	m Module
}

func (t *Txn) Module(name string) (*Scope, error) {
	for _, m := range t.db.reg.mods {
		if m.Name == name {
			return &Scope{t: t, m: m}, nil
		}
	}
	return nil, ErrUnknownModule
}

func (s *Scope) Exec(query string, args ...any) (sql.Result, error) {
	if s.t.done {
		return nil, ErrTxDone
	}
	if err := checkOwned(s.m, query); err != nil {
		return nil, err
	}
	return s.t.tx.ExecContext(s.t.ctx, query, args...)
}

// Query reads any table: other modules' data is read through their own
// interfaces by convention, but reads cannot corrupt ownership.
func (t *Txn) Query(query string, args ...any) (*sql.Rows, error) {
	if t.done {
		return nil, ErrTxDone
	}
	return t.tx.QueryContext(t.ctx, query, args...)
}

func (t *Txn) QueryRow(query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(t.ctx, query, args...)
}

// Delivered is an outbox event with its position.
type Delivered struct {
	Seq   int64
	Event Event
}

// Pending returns up to limit events after the consumer's cursor, oldest
// first. The cursor only moves on Ack, so a crash between handling and Ack
// replays the event: consumers must be idempotent (MD-05).
func (d *DB) Pending(ctx context.Context, consumer string, limit int) ([]Delivered, error) {
	if consumer == "" || limit <= 0 || limit > 10000 {
		return nil, ErrBadAck
	}
	rows, err := d.sql.QueryContext(ctx, `
SELECT o.seq, o.type, o.kind, o.id, o.version, o.digest FROM store_outbox o
WHERE o.seq > COALESCE((SELECT last_seq FROM store_cursor WHERE consumer = ?), 0)
ORDER BY o.seq LIMIT ?`, consumer, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivered
	for rows.Next() {
		var dl Delivered
		var kind string
		var version int64
		if err = rows.Scan(&dl.Seq, &dl.Event.Type, &kind, &dl.Event.Subject.ID, &version, &dl.Event.Subject.Digest); err != nil {
			return nil, err
		}
		dl.Event.Subject.Kind, dl.Event.Subject.Version = kernel.ArtifactKind(kind), uint64(version)
		out = append(out, dl)
	}
	return out, rows.Err()
}

// Ack moves the consumer's cursor forward to seq. It never moves backwards and
// never beyond an event that exists.
func (d *DB) Ack(ctx context.Context, consumer string, seq int64) error {
	if consumer == "" || seq <= 0 {
		return ErrBadAck
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var max, cur sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM store_outbox`).Scan(&max); err != nil {
		return err
	}
	if !max.Valid || seq > max.Int64 {
		return ErrBadAck
	}
	_ = tx.QueryRowContext(ctx, `SELECT last_seq FROM store_cursor WHERE consumer = ?`, consumer).Scan(&cur)
	if cur.Valid && seq < cur.Int64 {
		return ErrBadAck
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO store_cursor(consumer, last_seq) VALUES(?,?)
		ON CONFLICT(consumer) DO UPDATE SET last_seq = excluded.last_seq`, consumer, seq); err != nil {
		return err
	}
	return tx.Commit()
}

// Consume applies a bounded event batch and advances its consumer cursor in
// the same transaction. Projection handlers must write through this Txn; they
// must not start another DB/workspace transaction or perform external effects.
// Errors/panics roll back projection and cursor together, enabling safe replay.
func (d *DB) Consume(ctx context.Context, consumer string, limit int, apply func(*Txn, []Delivered) error) (int, error) {
	if consumer == "" || len(consumer) > 128 || limit <= 0 || limit > 1000 || apply == nil {
		return 0, ErrBadAck
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT seq,type,kind,id,version,digest FROM store_outbox WHERE seq > COALESCE((SELECT last_seq FROM store_cursor WHERE consumer=?),0) ORDER BY seq LIMIT ?`, consumer, limit)
	if err != nil {
		return 0, err
	}
	var events []Delivered
	for rows.Next() {
		var event Delivered
		if err = rows.Scan(&event.Seq, &event.Event.Type, &event.Event.Subject.Kind, &event.Event.Subject.ID, &event.Event.Subject.Version, &event.Event.Subject.Digest); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, event)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if len(events) == 0 {
		return 0, nil
	}
	if err = apply(tx, events); err != nil {
		return 0, err
	}
	if _, err = tx.tx.ExecContext(ctx, `INSERT INTO store_cursor(consumer,last_seq) VALUES(?,?) ON CONFLICT(consumer) DO UPDATE SET last_seq=excluded.last_seq`, consumer, events[len(events)-1].Seq); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(events), nil
}

// Trim removes events every given consumer has acknowledged. A consumer not in
// the list is not considered, so the caller must pass all registered ones.
func (d *DB) Trim(ctx context.Context, consumers []string) (int64, error) {
	if len(consumers) == 0 {
		return 0, ErrBadAck
	}
	low := int64(1<<62 - 1)
	for _, c := range consumers {
		var seq sql.NullInt64
		_ = d.sql.QueryRowContext(ctx, `SELECT last_seq FROM store_cursor WHERE consumer = ?`, c).Scan(&seq)
		if !seq.Valid {
			return 0, nil // a consumer that never acknowledged blocks trimming
		}
		if seq.Int64 < low {
			low = seq.Int64
		}
	}
	res, err := d.sql.ExecContext(ctx, `DELETE FROM store_outbox WHERE seq <= ?`, low)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

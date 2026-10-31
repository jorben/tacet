package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/jorben/tacet/internal/kernel"
)

var ctx = context.Background()

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mod(name, prefix string, sqls ...[]string) Module {
	m := Module{Name: name, Prefix: prefix}
	for i, s := range sqls {
		m.Migrations = append(m.Migrations, Migration{Version: i + 1, SQL: s})
	}
	return m
}

func jobs() Module {
	return mod("jobs", "jobs_", []string{`CREATE TABLE jobs_task (id TEXT PRIMARY KEY, state TEXT NOT NULL)`})
}
func findings() Module {
	return mod("findings", "findings_", []string{`CREATE TABLE findings_item (id INTEGER PRIMARY KEY, job TEXT)`, `CREATE INDEX findings_by_job ON findings_item(job)`})
}

func reg(t *testing.T, ms ...Module) *Registry {
	t.Helper()
	r, err := NewRegistry(ms...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func versions(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT module, version FROM store_schema`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var n string
		var v int
		rows.Scan(&n, &v)
		out[n] = v
	}
	return out
}

func TestRegistryRejectsInvalidDefinitions(t *testing.T) {
	bad := map[string][]Module{
		"bad name":         {{Name: "Jobs", Prefix: "jobs_"}},
		"prefix no suffix": {{Name: "jobs", Prefix: "jobs"}},
		"duplicate name":   {jobs(), jobs()},
		"overlap":          {jobs(), mod("jobs2", "jobs_x_")},
		"sparse versions":  {{Name: "a1", Prefix: "a1_", Migrations: []Migration{{Version: 2, SQL: []string{"x"}}}}},
		"empty migration":  {{Name: "a1", Prefix: "a1_", Migrations: []Migration{{Version: 1}}}},
	}
	for name, ms := range bad {
		if _, err := NewRegistry(ms...); !errors.Is(err, ErrMigrationSetup) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestMigrateFreshIdempotentAndIncremental(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "w.db"))
	d, err := Open(ctx, db, reg(t, jobs(), findings()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := versions(t, db); !reflect.DeepEqual(v, map[string]int{"jobs": 1, "findings": 1}) {
		t.Fatalf("%v", v)
	}
	// Running again changes nothing.
	if err = d.reg.Migrate(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	// A new build adds a second jobs migration; only that one runs.
	jobsV2 := mod("jobs", "jobs_", []string{`CREATE TABLE jobs_task (id TEXT PRIMARY KEY, state TEXT NOT NULL)`}, []string{`CREATE TABLE jobs_stage (id TEXT PRIMARY KEY)`})
	if _, err = Open(ctx, db, reg(t, jobsV2, findings()), nil); err != nil {
		t.Fatal(err)
	}
	if v := versions(t, db); v["jobs"] != 2 || v["findings"] != 1 {
		t.Fatalf("%v", v)
	}
	if _, err = db.Exec(`SELECT 1 FROM jobs_stage`); err != nil {
		t.Fatal("new table missing")
	}
}

func TestNewerSchemaOrUnknownModuleIsRefusedUntouched(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "w.db"))
	jobsV2 := mod("jobs", "jobs_", []string{`CREATE TABLE jobs_task (id TEXT PRIMARY KEY)`}, []string{`CREATE TABLE jobs_stage (id TEXT)`})
	if _, err := Open(ctx, db, reg(t, jobsV2), nil); err != nil {
		t.Fatal(err)
	}
	// An older build (version 1 only) opens the newer database.
	older := mod("jobs", "jobs_", []string{`CREATE TABLE jobs_task (id TEXT PRIMARY KEY)`})
	called := false
	_, err := Open(ctx, db, reg(t, older, findings()), func(context.Context) (func() error, func() error, error) { called = true; return nil, nil, nil })
	if !errors.Is(err, ErrSchemaTooNew) || called {
		t.Fatalf("older build opened a newer database: %v backup=%v", err, called)
	}
	if v := versions(t, db); v["jobs"] != 2 || len(v) != 1 {
		t.Fatalf("refused open modified the database: %v", v)
	}
	// Data of a module this build does not know is also refused.
	if _, err = Open(ctx, db, reg(t, findings()), nil); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("unknown module: %v", err)
	}
}

func TestFailedMigrationRollsBackEverythingAndRestoresBackup(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "w.db"))
	broken := mod("zzz", "zzz_", []string{`CREATE TABLE zzz_a (id INTEGER)`, `CREATE TABLE zzz_a (id INTEGER)`}) // second statement fails
	restored, discarded := false, false
	backup := func(context.Context) (func() error, func() error, error) {
		return func() error { restored = true; return nil }, func() error { discarded = true; return nil }, nil
	}
	_, err := Open(ctx, db, reg(t, jobs(), broken), backup)
	if !errors.Is(err, ErrMigrationFail) || !restored || discarded {
		t.Fatalf("err=%v restored=%v discarded=%v", err, restored, discarded)
	}
	// The earlier module of the same run was rolled back too.
	if _, e := db.Exec(`SELECT 1 FROM jobs_task`); e == nil {
		t.Fatal("partial migration left tables behind")
	}
	var coreTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('store_schema','store_outbox','store_cursor')`).Scan(&coreTables); err != nil {
		t.Fatal(err)
	}
	if coreTables != 0 {
		t.Fatal("failed first migration left core tables behind")
	}
	// A successful run discards the backup; a failing backup stops before any change.
	restored = false
	if _, err = Open(ctx, db, reg(t, jobs()), backup); err != nil || restored || !discarded {
		t.Fatalf("success path: %v restored=%v discarded=%v", err, restored, discarded)
	}
	db2 := rawDB(t, filepath.Join(t.TempDir(), "x.db"))
	failing := func(context.Context) (func() error, func() error, error) { return nil, nil, errors.New("disk full") }
	if _, err = Open(ctx, db2, reg(t, jobs()), failing); !errors.Is(err, ErrBackupFailed) {
		t.Fatalf("backup failure: %v", err)
	}
	if _, e := db2.Exec(`SELECT 1 FROM jobs_task`); e == nil {
		t.Fatal("migration ran although the backup failed")
	}
}

func TestMigrationRefusalDoesNotBootstrapCoreTables(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "future.db"))
	if _, err := db.Exec(`CREATE TABLE store_schema(module TEXT PRIMARY KEY,version INTEGER NOT NULL); INSERT INTO store_schema VALUES('jobs',99)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, db, reg(t, jobs()), nil); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("future schema: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('store_outbox','store_cursor')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("refused migration modified future database")
	}
	fresh := rawDB(t, filepath.Join(t.TempDir(), "fresh.db"))
	backup := func(context.Context) (func() error, func() error, error) {
		var tables int
		if err := fresh.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table'`).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if tables != 0 {
			t.Fatal("core bootstrap occurred before backup")
		}
		return nil, nil, errors.New("fixture backup denied")
	}
	if _, err := Open(ctx, fresh, reg(t, jobs()), backup); !errors.Is(err, ErrBackupFailed) {
		t.Fatal(err)
	}
}

func TestMigrationCannotWriteAnotherModulesTables(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "w.db"))
	sneaky := mod("sneaky", "sneaky_", []string{`CREATE TABLE sneaky_ok (id INTEGER)`}, []string{`ALTER TABLE jobs_task ADD COLUMN x TEXT`})
	if _, err := Open(ctx, db, reg(t, jobs(), sneaky), nil); !errors.Is(err, ErrMigrationFail) {
		t.Fatalf("foreign DDL accepted: %v", err)
	}
}

func openJobs(t *testing.T, path string) *DB {
	t.Helper()
	d, err := Open(ctx, rawDB(t, path), reg(t, jobs(), findings()), nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestScopeOwnershipGuard(t *testing.T) {
	d := openJobs(t, filepath.Join(t.TempDir(), "w.db"))
	tx, _ := d.Begin(ctx)
	defer tx.Rollback()
	s, err := tx.Module("jobs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exec(`INSERT INTO jobs_task(id, state) VALUES(?, ?)`, "j1", "queued"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exec(`UPDATE jobs_task SET state = ? WHERE id = ?`, "running", "j1"); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"other module insert":   `INSERT INTO findings_item(job) VALUES('j1')`,
		"other module delete":   `DELETE FROM findings_item`,
		"other module update":   `UPDATE findings_item SET job = 'x'`,
		"index on foreign":      `CREATE INDEX jobs_sneak ON findings_item(job)`,
		"drop foreign":          `DROP TABLE findings_item`,
		"outbox write":          `INSERT INTO store_outbox(type,kind,id,version,digest) VALUES('x','k','i',1,'d')`,
		"two statements":        `INSERT INTO jobs_task(id,state) VALUES('a','b'); DELETE FROM findings_item`,
		"select is not a write": `SELECT * FROM jobs_task`,
		"prefix lookalike":      `INSERT INTO jobsx(id) VALUES(1)`,
	} {
		if _, err = s.Exec(q); !errors.Is(err, ErrNotOwner) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if _, err = tx.Module("ghost"); !errors.Is(err, ErrUnknownModule) {
		t.Fatal("unknown module scope")
	}
}

func TestTransactionCommitRollbackAndHooks(t *testing.T) {
	d := openJobs(t, filepath.Join(t.TempDir(), "w.db"))
	hooks := 0
	tx, _ := d.Begin(ctx)
	s, _ := tx.Module("jobs")
	s.Exec(`INSERT INTO jobs_task(id, state) VALUES('keep','q')`)
	tx.AfterCommit(func() { hooks++ })
	tx.Emit(Event{Type: "JobStateChanged", Subject: kernel.ArtifactRef{ID: "keep"}})
	if hooks != 0 {
		t.Fatal("hook ran before commit")
	}
	if err := tx.Commit(); err != nil || hooks != 1 {
		t.Fatalf("commit: %v hooks=%d", err, hooks)
	}
	if err := tx.Commit(); !errors.Is(err, ErrTxDone) {
		t.Fatal("double commit")
	}
	tx, _ = d.Begin(ctx)
	s, _ = tx.Module("jobs")
	s.Exec(`INSERT INTO jobs_task(id, state) VALUES('drop','q')`)
	tx.AfterCommit(func() { hooks++ })
	tx.Emit(Event{Type: "JobStateChanged", Subject: kernel.ArtifactRef{ID: "drop"}})
	if err := tx.Rollback(); err != nil || hooks != 1 {
		t.Fatalf("rollback: %v hooks=%d", err, hooks)
	}
	if _, err := s.Exec(`INSERT INTO jobs_task(id, state) VALUES('late','q')`); !errors.Is(err, ErrTxDone) {
		t.Fatal("write after rollback")
	}
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM jobs_task`).Scan(&n)
	pending, _ := d.Pending(ctx, "c", 10)
	if n != 1 || len(pending) != 1 || pending[0].Event.Subject.ID != "keep" {
		t.Fatalf("rows=%d events=%v: the event must exist if and only if the change does", n, pending)
	}
}

func TestEmitFailureFailsTheCommitAndRollsBackTheChange(t *testing.T) {
	d := openJobs(t, filepath.Join(t.TempDir(), "w.db"))
	if _, err := d.sql.Exec(`DROP TABLE store_outbox`); err != nil {
		t.Fatal(err)
	}
	tx, _ := d.Begin(ctx)
	s, _ := tx.Module("jobs")
	s.Exec(`INSERT INTO jobs_task(id, state) VALUES('x','q')`)
	ran := false
	tx.AfterCommit(func() { ran = true })
	tx.Emit(Event{Type: "T"})
	if err := tx.Commit(); err == nil || ran {
		t.Fatalf("a lost event must fail the commit: %v hook=%v", err, ran)
	}
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM jobs_task`).Scan(&n)
	if n != 0 {
		t.Fatal("change committed although its event could not be written")
	}
}

func emit(t *testing.T, d *DB, ids ...string) {
	t.Helper()
	tx, _ := d.Begin(ctx)
	for _, id := range ids {
		tx.Emit(Event{Type: "E", Subject: kernel.ArtifactRef{Kind: "k", ID: id, Version: 7, Digest: "dg"}})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxOrderingCursorsReplayAndAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	d := openJobs(t, path)
	emit(t, d, "a", "b", "c")
	got, err := d.Pending(ctx, "query", 2)
	if err != nil || len(got) != 2 || got[0].Event.Subject.ID != "a" || got[1].Event.Subject.ID != "b" || got[0].Seq >= got[1].Seq {
		t.Fatalf("%+v %v", got, err)
	}
	if e := got[0].Event.Subject; e.Version != 7 || e.Digest != "dg" || e.Kind != "k" {
		t.Fatalf("event fields lost: %+v", e)
	}
	// Without Ack the same events come again (crash replay).
	again, _ := d.Pending(ctx, "query", 2)
	if !reflect.DeepEqual(got, again) {
		t.Fatal("pending is not idempotent")
	}
	if err = d.Ack(ctx, "query", got[1].Seq); err != nil {
		t.Fatal(err)
	}
	// Cursors are per consumer.
	other, _ := d.Pending(ctx, "retention", 10)
	next, _ := d.Pending(ctx, "query", 10)
	if len(other) != 3 || len(next) != 1 || next[0].Event.Subject.ID != "c" {
		t.Fatalf("other=%d next=%v", len(other), next)
	}
	for name, seq := range map[string]int64{"backwards": got[0].Seq, "beyond last": 999, "zero": 0} {
		if err = d.Ack(ctx, "query", seq); !errors.Is(err, ErrBadAck) {
			t.Errorf("%s ack accepted: %v", name, err)
		}
	}
	if _, err = d.Pending(ctx, "", 1); !errors.Is(err, ErrBadAck) {
		t.Fatal("empty consumer")
	}
	// The cursor survives a restart of the process (reopen the file).
	d.Close()
	d2 := openJobs(t, path)
	after, _ := d2.Pending(ctx, "query", 10)
	if len(after) != 1 || after[0].Event.Subject.ID != "c" {
		t.Fatalf("cursor lost across reopen: %v", after)
	}
}

func TestTrimKeepsEventsTheSlowestConsumerHasNotAcknowledged(t *testing.T) {
	d := openJobs(t, filepath.Join(t.TempDir(), "w.db"))
	emit(t, d, "a", "b", "c")
	all, _ := d.Pending(ctx, "x", 10)
	d.Ack(ctx, "fast", all[2].Seq)
	if n, _ := d.Trim(ctx, []string{"fast", "slow"}); n != 0 {
		t.Fatalf("trimmed %d events before slow ever acknowledged", n)
	}
	d.Ack(ctx, "slow", all[0].Seq)
	if n, err := d.Trim(ctx, []string{"fast", "slow"}); err != nil || n != 1 {
		t.Fatalf("trim %d %v", n, err)
	}
	rest, _ := d.Pending(ctx, "slow", 10)
	if len(rest) != 2 || rest[0].Event.Subject.ID != "b" {
		t.Fatalf("%v", rest)
	}
	if _, err := d.Trim(ctx, nil); !errors.Is(err, ErrBadAck) {
		t.Fatal("trim without consumers")
	}
}

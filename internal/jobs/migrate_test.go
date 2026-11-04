package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// These tests talk to the store directly (no workspace), so they run on every platform.

const (
	parkingVersion  = 5
	parallelVersion = 6 // the last version: nothing limits a kind any more
)

// moduleAt is this module as a build that only knew the migrations up to version.
func moduleAt(version int) store.Module {
	m := Module()
	m.Migrations = m.Migrations[:version]
	return m
}

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func openAt(t *testing.T, raw *sql.DB, m store.Module, backup store.Backup) (*store.DB, error) {
	t.Helper()
	reg, err := store.NewRegistry(m)
	if err != nil {
		t.Fatal(err)
	}
	return store.Open(t.Context(), raw, reg, backup)
}

// oldDatabase is a database as version 4 left it: one confirmed batch waiting for a person, paused by the last
// exit, and one quick task awaiting review.
func oldDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, moduleAt(parkingVersion-1), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO jobs_batch(id,kind,confirmed,snapshot_version) VALUES('b1','regular',1,7),('q1','quick',1,8)`,
		`INSERT INTO jobs_job(id,batch,ordinal,version,attempt,position) VALUES('j1','b1',0,9,'','{"state":"paused","resume":"awaiting_review"}'),('j2','q1',0,3,'','{"state":"awaiting_review"}')`,
	} {
		if _, err = raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return path
}

func inTxn(t *testing.T, db *store.DB, fn func(*store.Txn) error) error {
	t.Helper()
	tx, err := db.Begin(context.WithoutCancel(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func countBackups(n *int) store.Backup {
	return func(context.Context) (func() error, func() error, error) {
		*n++
		return func() error { return nil }, func() error { return nil }, nil
	}
}

func objectExists(t *testing.T, raw *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func columnExists(t *testing.T, raw *sql.DB, column string) bool {
	t.Helper()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('jobs_batch') WHERE name=?`, column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func schemaVersion(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var v int
	if err := raw.QueryRow(`SELECT version FROM store_schema WHERE module='jobs'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAnOlderDatabaseKeepsItsWorkAndRunsTasksSideBySide(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	backups := 0
	db, err := openAt(t, raw, Module(), countBackups(&backups))
	if err != nil {
		t.Fatal(err)
	}
	if backups != 1 {
		t.Fatalf("the migration took %d backups, want 1", backups)
	}
	var confirmed, snapshot int
	if err = raw.QueryRow(`SELECT confirmed,snapshot_version FROM jobs_batch WHERE id='b1'`).Scan(&confirmed, &snapshot); err != nil {
		t.Fatal(err)
	}
	if confirmed != 1 || snapshot != 7 {
		t.Fatalf("confirmed %d snapshot %d", confirmed, snapshot)
	}
	repo := NewRepository(nil, DefaultLimits())
	if err = inTxn(t, db, func(tx *store.Txn) error {
		j, err := repo.Get(tx, "j1")
		if err != nil {
			return err
		}
		if j.Version != 9 || j.Position.State != Paused || j.Position.Resume != AwaitingReview {
			t.Errorf("the waiting file changed: %+v", j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if objectExists(t, raw, "jobs_one_active_kind") || columnExists(t, raw, "parked") {
		t.Fatal("the limit per kind is still in the schema")
	}
	// tasks of a kind that already has an unfinished one go beside it
	for _, kind := range []BatchKind{RegularBatch, QuickBatch} {
		if err = inTxn(t, db, func(tx *store.Txn) error { return repo.CreateDraft(tx, "next-"+string(kind), kind) }); err != nil {
			t.Fatalf("%s beside an older one: %v", kind, err)
		}
	}
	// nor does the database itself keep one out when the repository is not asked
	if _, err = raw.Exec(`INSERT INTO jobs_batch(id,kind) VALUES('direct','regular')`); err != nil {
		t.Fatalf("the database refused a second batch of a kind: %v", err)
	}
	if err = inTxn(t, db, func(tx *store.Txn) error {
		bs, err := repo.Unfinished(tx)
		if len(bs) != 5 {
			t.Errorf("unfinished %+v", bs)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// the migrated schema is recorded, so a second start does not migrate again
	if _, err = openAt(t, raw, Module(), func(context.Context) (func() error, func() error, error) {
		t.Error("a current database was migrated again")
		return nil, nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if v := schemaVersion(t, raw); v != parallelVersion {
		t.Fatalf("version %d", v)
	}
}

// What the parking build left: parked batches beside the one holding the slot. Every one is still there, as it was.
func TestAParkingDatabaseKeepsEveryTask(t *testing.T) {
	path := parkingDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	backups := 0
	db, err := openAt(t, raw, Module(), countBackups(&backups))
	if err != nil {
		t.Fatal(err)
	}
	if backups != 1 {
		t.Fatalf("the migration took %d backups, want 1", backups)
	}
	repo := NewRepository(nil, DefaultLimits())
	if err = inTxn(t, db, func(tx *store.Txn) error {
		bs, err := repo.Unfinished(tx)
		if err != nil {
			return err
		}
		var got []string
		for _, b := range bs {
			got = append(got, b.ID+":"+string(b.Kind))
		}
		if want := "p1:regular p2:regular b1:regular q1:quick"; strings.Join(got, " ") != want {
			t.Errorf("unfinished %v, want %s", got, want)
		}
		for id, version := range map[string]uint64{"jp1": 4, "jp2": 5, "j1": 9} {
			j, err := repo.Get(tx, id)
			if err != nil {
				return err
			}
			if j.Version != version {
				t.Errorf("%s changed: %+v", id, j)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// a finished batch stays finished
	var finished int
	if err = raw.QueryRow(`SELECT finished FROM jobs_batch WHERE id='done'`).Scan(&finished); err != nil || finished != 1 {
		t.Fatalf("the finished batch: %d %v", finished, err)
	}
}

// parkingDatabase is a database as version 5 left it: two parked batches, the holders of both slots and a finished one.
func parkingDatabase(t *testing.T) string {
	t.Helper()
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, moduleAt(parkingVersion), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DELETE FROM jobs_job`, `DELETE FROM jobs_batch`,
		`INSERT INTO jobs_batch(id,kind,confirmed,snapshot_version,parked) VALUES('p1','regular',1,5,1),('p2','regular',1,6,1),('b1','regular',1,7,0),('q1','quick',1,8,0)`,
		`INSERT INTO jobs_batch(id,kind,finished,confirmed) VALUES('done','regular',1,1)`,
		`INSERT INTO jobs_job(id,batch,ordinal,version,attempt,position) VALUES('jp1','p1',0,4,'','{"state":"awaiting_review"}'),('jp2','p2',0,5,'','{"state":"ready_to_publish"}'),('j1','b1',0,9,'','{"state":"queued"}'),('j2','q1',0,3,'','{"state":"exporting"}')`,
	} {
		if _, err = raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return path
}

// The upgrade runs in one transaction. Failing on its last statement (an index on the column, which SQLite refuses to
// drop) takes back the index it had already dropped: the limit per kind holds again, the version stays and the backup
// is put back.
func TestAFailedUpgradeLeavesTheParkingDatabaseAsItWas(t *testing.T) {
	path := parkingDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	if _, err := raw.Exec(`CREATE INDEX jobs_in_the_way ON jobs_batch(parked)`); err != nil {
		t.Fatal(err)
	}
	restored, discarded := 0, 0
	_, err := openAt(t, raw, Module(), func(context.Context) (func() error, func() error, error) {
		return func() error { restored++; return nil }, func() error { discarded++; return nil }, nil
	})
	if !errors.Is(err, store.ErrMigrationFail) {
		t.Fatalf("the upgrade: %v", err)
	}
	if restored != 1 || discarded != 0 {
		t.Fatalf("backup restored %d, discarded %d", restored, discarded)
	}
	if v := schemaVersion(t, raw); v != parkingVersion {
		t.Fatalf("version %d", v)
	}
	if !objectExists(t, raw, "jobs_one_active_kind") {
		t.Fatal("the dropped index did not come back with the rollback")
	}
	if !columnExists(t, raw, "parked") {
		t.Fatal("the column did not survive the rollback")
	}
	var parked int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM jobs_batch WHERE parked=1`).Scan(&parked); err != nil || parked != 2 {
		t.Fatalf("parked %d %v", parked, err)
	}
	if _, err = raw.Exec(`INSERT INTO jobs_batch(id,kind) VALUES('sneak','regular')`); err == nil {
		t.Fatal("the restored index did not hold")
	}
}

// Several versions in one go are one transaction too: a failure in version 5 leaves version 4 whole.
func TestAFailedUpgradeLeavesTheOlderDatabaseAsItWas(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	// without the index, DROP INDEX fails after ALTER TABLE succeeded
	if _, err := raw.Exec(`DROP INDEX jobs_one_active_kind`); err != nil {
		t.Fatal(err)
	}
	restored, discarded := 0, 0
	_, err := openAt(t, raw, Module(), func(context.Context) (func() error, func() error, error) {
		return func() error { restored++; return nil }, func() error { discarded++; return nil }, nil
	})
	if !errors.Is(err, store.ErrMigrationFail) {
		t.Fatalf("the upgrade: %v", err)
	}
	if restored != 1 || discarded != 0 {
		t.Fatalf("backup restored %d, discarded %d", restored, discarded)
	}
	if v := schemaVersion(t, raw); v != parkingVersion-1 {
		t.Fatalf("version %d", v)
	}
	if columnExists(t, raw, "parked") {
		t.Fatal("the parked column survived the rollback")
	}
	var rows int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM jobs_batch`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("batches %d %v", rows, err)
	}
}

// A database of a newer build is refused before anything changes (AR-18): the parking build cannot open a migrated
// workspace, which the release notes must say.
func TestAnOlderBuildRefusesAMigratedDatabase(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	if _, err := openAt(t, raw, Module(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := openAt(t, raw, moduleAt(parallelVersion-1), nil); !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("an older build: %v", err)
	}
}

// A draft is part of the transaction that makes it: a failure takes it and its event back, and others stay.
func TestAFailureAfterADraftTakesItBack(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, Module(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(nil, DefaultLimits())
	boom := errors.New("members failed")
	if err = inTxn(t, db, func(tx *store.Txn) error {
		if err := repo.CreateDraft(tx, "next", RegularBatch); err != nil {
			t.Fatal(err)
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	var drafts, events, others int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM jobs_batch WHERE id='next'`).Scan(&drafts); err != nil || drafts != 0 {
		t.Fatalf("drafts %d %v", drafts, err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM store_outbox WHERE id='next'`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("events %d %v", events, err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM jobs_batch WHERE finished=0`).Scan(&others); err != nil || others != 2 {
		t.Fatalf("the others %d %v", others, err)
	}
}

// Without members nothing can be listed or continued: what an import stopped between its transactions leaves is
// skipped by the callers and does not concern the next import.
func TestADraftWithoutMembersDoesNotConcernTheNextOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, Module(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(nil, DefaultLimits())
	for _, id := range []string{"left", "next"} {
		if err = inTxn(t, db, func(tx *store.Txn) error { return repo.CreateDraft(tx, id, RegularBatch) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err = inTxn(t, db, func(tx *store.Txn) error { return repo.AddMember(tx, "next", "j2", 0) }); err != nil {
		t.Fatal(err)
	}
}

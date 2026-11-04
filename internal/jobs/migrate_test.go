package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// These tests talk to the store directly (no workspace), so they run on every platform.

const parkingVersion = 5

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

func TestAnOlderDatabaseKeepsItsWorkAndGainsParking(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	backups := 0
	db, err := openAt(t, raw, Module(), func(context.Context) (func() error, func() error, error) {
		backups++
		return func() error { return nil }, func() error { return nil }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if backups != 1 {
		t.Fatalf("the migration took %d backups, want 1", backups)
	}
	var parked, confirmed, snapshot int
	if err = raw.QueryRow(`SELECT parked,confirmed,snapshot_version FROM jobs_batch WHERE id='b1'`).Scan(&parked, &confirmed, &snapshot); err != nil {
		t.Fatal(err)
	}
	if parked != 0 || confirmed != 1 || snapshot != 7 {
		t.Fatalf("parked %d confirmed %d snapshot %d", parked, confirmed, snapshot)
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
	// Until something parks them the upgraded batches hold their slots, as they did.
	for _, tc := range []struct {
		kind BatchKind
		want error
	}{{RegularBatch, ErrBatchActive}, {QuickBatch, ErrQuickActive}} {
		if err = inTxn(t, db, func(tx *store.Txn) error { return repo.CreateDraft(tx, "next-"+string(tc.kind), tc.kind) }); !errors.Is(err, tc.want) {
			t.Fatalf("%s before parking: %v", tc.kind, err)
		}
	}
	var before int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM store_outbox WHERE id='b1' AND type='BatchChanged'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A task that was waiting before the upgrade is parked by the next import, like any other.
	for _, tc := range []struct {
		kind BatchKind
		held string
	}{{RegularBatch, "b1"}, {QuickBatch, "q1"}} {
		if err = inTxn(t, db, func(tx *store.Txn) error {
			got, err := repo.ParkHolder(tx, tc.kind, func(string) (bool, error) { return false, nil })
			if err != nil {
				return err
			}
			if got != tc.held {
				t.Errorf("parked %q, want %q", got, tc.held)
			}
			return repo.CreateDraft(tx, "next-"+string(tc.kind), tc.kind)
		}); err != nil {
			t.Fatalf("%s after parking: %v", tc.kind, err)
		}
	}
	// parking is announced, like every other change of a batch
	var after int
	if err = raw.QueryRow(`SELECT COUNT(*) FROM store_outbox WHERE id='b1' AND type='BatchChanged'`).Scan(&after); err != nil || after != before+1 {
		t.Fatalf("events %d -> %d %v", before, after, err)
	}
	// the constraint itself: a second unparked row of a kind is refused without going through the repository
	if _, err = raw.Exec(`INSERT INTO jobs_batch(id,kind) VALUES('sneak','regular')`); err == nil {
		t.Fatal("the unique index let a second unparked batch of a kind in")
	}
	// the migrated schema is recorded, so a second start does not migrate again
	if _, err = openAt(t, raw, Module(), func(context.Context) (func() error, func() error, error) {
		t.Error("a current database was migrated again")
		return nil, nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = raw.QueryRow(`SELECT version FROM store_schema WHERE module='jobs'`).Scan(&version); err != nil || version != parkingVersion {
		t.Fatalf("version %d %v", version, err)
	}
}

// The upgrade runs in one transaction: when its last statements fail, the column it already added is gone again,
// the version stays and the backup is put back.
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
	var version, columns, rows int
	if err = raw.QueryRow(`SELECT version FROM store_schema WHERE module='jobs'`).Scan(&version); err != nil || version != parkingVersion-1 {
		t.Fatalf("version %d %v", version, err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('jobs_batch') WHERE name='parked'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("the parked column survived the rollback: %d %v", columns, err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM jobs_batch`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("batches %d %v", rows, err)
	}
}

// A database of a newer build is refused before anything changes (AR-18): an older build cannot open a parked
// workspace, which the release notes must say.
func TestAnOlderBuildRefusesAParkingDatabase(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	if _, err := openAt(t, raw, Module(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := openAt(t, raw, moduleAt(parkingVersion-1), nil); !errors.Is(err, store.ErrSchemaTooNew) {
		t.Fatalf("an older build: %v", err)
	}
}

// A failure after the park in the same transaction takes the park back, with its event.
func TestAFailureAfterParkingLeavesTheHolderHoldingTheSlot(t *testing.T) {
	path := oldDatabase(t)
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, Module(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(nil, DefaultLimits())
	boom := errors.New("draft failed")
	if err = inTxn(t, db, func(tx *store.Txn) error {
		got, err := repo.ParkHolder(tx, RegularBatch, func(string) (bool, error) { return false, nil })
		if err != nil || got != "b1" {
			t.Fatalf("parked %q %v", got, err)
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	var parked, events int
	if err = raw.QueryRow(`SELECT parked FROM jobs_batch WHERE id='b1'`).Scan(&parked); err != nil || parked != 0 {
		t.Fatalf("parked %d %v", parked, err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM store_outbox WHERE id='b1' AND type='BatchChanged'`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("events %d %v", events, err)
	}
}

// An import that stops after creating its draft and before adding the files leaves a draft without members. Nothing
// lists it or can finish it, so the next import that makes room takes the slot back.
func TestADraftWithoutMembersGivesUpTheSlot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw := rawDB(t, path)
	defer raw.Close()
	db, err := openAt(t, raw, Module(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(nil, DefaultLimits())
	waits := func(string) (bool, error) { return false, nil }
	if err = inTxn(t, db, func(tx *store.Txn) error { return repo.CreateDraft(tx, "left", RegularBatch) }); err != nil {
		t.Fatal(err)
	}
	if err = inTxn(t, db, func(tx *store.Txn) error {
		got, err := repo.ParkHolder(tx, RegularBatch, waits)
		if err != nil || got != "" {
			t.Fatalf("parked %q %v", got, err)
		}
		return repo.CreateDraft(tx, "next", RegularBatch)
	}); err != nil {
		t.Fatalf("the draft without members kept the slot: %v", err)
	}
	var finished, parked int
	if err = raw.QueryRow(`SELECT finished,parked FROM jobs_batch WHERE id='left'`).Scan(&finished, &parked); err != nil || finished != 1 || parked != 0 {
		t.Fatalf("the draft: finished %d parked %d %v", finished, parked, err)
	}
	// an import still under way behind it finds out here, and adds nothing to a batch nobody sees
	if err = inTxn(t, db, func(tx *store.Txn) error { return repo.AddMember(tx, "left", "j1", 0) }); !errors.Is(err, ErrJobRecord) {
		t.Fatalf("a member joined the finished draft: %v", err)
	}
	// a draft with a member is somebody's work: it keeps the slot
	if err = inTxn(t, db, func(tx *store.Txn) error { return repo.AddMember(tx, "next", "j2", 0) }); err != nil {
		t.Fatal(err)
	}
	if err = inTxn(t, db, func(tx *store.Txn) error {
		if got, err := repo.ParkHolder(tx, RegularBatch, waits); err != nil || got != "" {
			t.Fatalf("parked %q %v", got, err)
		}
		return repo.CreateDraft(tx, "third", RegularBatch)
	}); !errors.Is(err, ErrBatchActive) {
		t.Fatalf("the draft with a member gave up the slot: %v", err)
	}
}

package coverage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func ledgerDB(t *testing.T) func(fn func(store.Tx) error) error {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "c.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return func(fn func(store.Tx) error) error {
		tx, err := db.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = fn(tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}
}

func ledgerPart(id string, class ContentClass, ins Inspection) Part {
	return Part{ID: id, Class: class, Required: class == ClassText, Known: class != ClassOther, Bounded: true, Inspection: ins, Disposition: Pending, Validation: NotRun}
}

func TestLedgerRoundTripsAndFeedsAssess(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	if err := do(func(tx store.Tx) error {
		if err := r.Begin(tx, "j1", "a1"); err != nil {
			return err
		}
		for _, p := range []Part{ledgerPart("word/document.xml", ClassText, Inspected), ledgerPart("custom/blob.bin", ClassOther, NotInspected)} {
			if p.ID == "custom/blob.bin" {
				p.Known = false
			}
			if err := r.Record(tx, "j1", "a1", p); err != nil {
				return err
			}
		}
		return r.Complete(tx, "j1", "a1")
	}); err != nil {
		t.Fatal(err)
	}
	var parts []Part
	var complete bool
	if err := do(func(tx store.Tx) (err error) { parts, complete, err = r.Ledger(tx, "j1"); return err }); err != nil {
		t.Fatal(err)
	}
	if !complete || len(parts) != 2 || parts[0].ID != "custom/blob.bin" || parts[1].Inspection != Inspected {
		t.Fatalf("%+v complete=%v", parts, complete)
	}
	report, err := Assess(parts, complete)
	if err != nil || len(report.Unresolved) != 1 || report.Unresolved[0].PartID != "custom/blob.bin" || report.Unresolved[0].Kind != GapUninspectedPart {
		t.Fatalf("the unknown part must be a gap: %+v %v", report, err)
	}
}

func TestAnUnfinishedEnumerationIsNeverTakenForAGapList(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	do(func(tx store.Tx) error {
		r.Begin(tx, "j1", "a1")
		return r.Record(tx, "j1", "a1", ledgerPart("p", ClassText, Inspected))
	})
	var parts []Part
	var complete bool
	do(func(tx store.Tx) (err error) { parts, complete, err = r.Ledger(tx, "j1"); return err })
	if complete {
		t.Fatal("an attempt that never completed reads as complete")
	}
	if _, err := Assess(parts, complete); err == nil {
		t.Fatal("Assess accepted an incomplete inventory")
	}
}

func TestOnlyTheCurrentAttemptMayWriteAndANewAttemptStartsClean(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	do(func(tx store.Tx) error {
		r.Begin(tx, "j1", "a1")
		return r.Record(tx, "j1", "a1", ledgerPart("old", ClassText, Inspected))
	})
	if err := do(func(tx store.Tx) error { return r.Begin(tx, "j1", "a2") }); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func(tx store.Tx) error{
		"late record":              func(tx store.Tx) error { return r.Record(tx, "j1", "a1", ledgerPart("late", ClassText, Inspected)) },
		"late complete":            func(tx store.Tx) error { return r.Complete(tx, "j1", "a1") },
		"same attempt begun twice": func(tx store.Tx) error { return r.Begin(tx, "j1", "a2") },
	} {
		if err := do(call); !errors.Is(err, ErrStaleAttempt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var parts []Part
	do(func(tx store.Tx) (err error) { parts, _, err = r.Ledger(tx, "j1"); return err })
	if len(parts) != 0 {
		t.Fatalf("rows of the abandoned attempt survived: %+v", parts)
	}
}

func TestLedgerRefusesInvalidPartsClosedLedgersAndUnknownJobs(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	do(func(tx store.Tx) error { return r.Begin(tx, "j1", "a1") })
	bad := []Part{{}, {ID: "p", Class: "weird", Inspection: Inspected, Disposition: Pending, Validation: NotRun},
		{ID: "p", Class: ClassText, Inspection: "maybe", Disposition: Pending, Validation: NotRun},
		{ID: "p", Class: ClassText, Inspection: Inspected, Disposition: "gone", Validation: NotRun},
		{ID: "p", Class: ClassText, Inspection: Inspected, Disposition: Pending, Validation: "ok"}}
	for i, p := range bad {
		if err := do(func(tx store.Tx) error { return r.Record(tx, "j1", "a1", p) }); !errors.Is(err, ErrLedger) {
			t.Errorf("bad part %d accepted: %v", i, err)
		}
	}
	do(func(tx store.Tx) error { return r.Complete(tx, "j1", "a1") })
	if err := do(func(tx store.Tx) error { return r.Record(tx, "j1", "a1", ledgerPart("p", ClassText, Inspected)) }); !errors.Is(err, ErrLedger) {
		t.Errorf("a closed ledger accepted a part: %v", err)
	}
	if err := do(func(tx store.Tx) error { return r.Record(tx, "nope", "a1", ledgerPart("p", ClassText, Inspected)) }); !errors.Is(err, ErrNoInventory) {
		t.Errorf("unknown job: %v", err)
	}
	if err := do(func(tx store.Tx) error { _, _, err := r.Ledger(tx, "nope"); return err }); !errors.Is(err, ErrNoInventory) {
		t.Errorf("unknown ledger: %v", err)
	}
	if err := do(func(tx store.Tx) error { return r.Begin(tx, "", "a") }); !errors.Is(err, ErrLedger) {
		t.Errorf("empty job: %v", err)
	}
}

func TestRecordingAPartAgainReplacesItAndPurgeRemovesTheJob(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	do(func(tx store.Tx) error {
		r.Begin(tx, "j1", "a1")
		r.Begin(tx, "j2", "b1")
		r.Record(tx, "j1", "a1", ledgerPart("p", ClassText, NotInspected))
		r.Record(tx, "j1", "a1", ledgerPart("p", ClassText, Inspected))
		r.Record(tx, "j2", "b1", ledgerPart("p", ClassText, Inspected))
		return r.Complete(tx, "j1", "a1")
	})
	var parts []Part
	do(func(tx store.Tx) (err error) { parts, _, err = r.Ledger(tx, "j1"); return err })
	if len(parts) != 1 || parts[0].Inspection != Inspected {
		t.Fatalf("%+v", parts)
	}
	do(func(tx store.Tx) error { return r.Purge(tx, "j1") })
	if err := do(func(tx store.Tx) error { _, _, err := r.Ledger(tx, "j1"); return err }); !errors.Is(err, ErrNoInventory) {
		t.Fatal("purged ledger still readable")
	}
	if err := do(func(tx store.Tx) error { _, _, err := r.Ledger(tx, "j2"); return err }); err != nil {
		t.Fatalf("another job's ledger was purged: %v", err)
	}
}

func TestANewAttemptIsIncompleteEvenAfterAnEarlierOneCompleted(t *testing.T) {
	do := ledgerDB(t)
	var r Repository
	do(func(tx store.Tx) error {
		r.Begin(tx, "j1", "a1")
		r.Record(tx, "j1", "a1", ledgerPart("p", ClassText, Inspected))
		return r.Complete(tx, "j1", "a1")
	})
	do(func(tx store.Tx) error { return r.Begin(tx, "j1", "a2") })
	var complete bool
	do(func(tx store.Tx) (err error) { _, complete, err = r.Ledger(tx, "j1"); return err })
	if complete {
		t.Fatal("the redone extraction reads as complete before it finished")
	}
}

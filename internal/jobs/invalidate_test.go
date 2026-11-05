//go:build darwin || windows

package jobs

import (
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/lineage"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

func facetSet(detect, dispose, render, runtime, delivery string) map[kernel.Facet]string {
	return map[kernel.Facet]string{kernel.FacetDetect: detect, kernel.FacetDispose: dispose,
		kernel.FacetRender: render, kernel.FacetRuntime: runtime, kernel.FacetDelivery: delivery}
}

// reads builds the lineage record a stage start would store for the given facets.
func reads(f map[kernel.Facet]string) StageReads {
	return StageReads{Input: "in1", Decisions: 1, Facets: f}
}

func (f *fixture) startReading(t *testing.T, id string, r StageReads) kernel.AttemptRef {
	t.Helper()
	job := f.get(t, id)
	var attempt kernel.AttemptRef
	if err := f.commit(func(tx store.Tx) error {
		var err error
		attempt, err = f.repo.BeginStageReading(tx, id, f.due(t, job), job.Version, f.ws.Generation(), r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return attempt
}

// preflighted completes the (plain) preflight of each member.
func (f *fixture) preflighted(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		f.complete(t, f.start(t, id), sched.StageOutcome{Kind: sched.Completed})
	}
}

func (f *fixture) stage(t *testing.T, id string, r StageReads) Job {
	t.Helper()
	return f.complete(t, f.startReading(t, id, r), sched.StageOutcome{Kind: sched.Completed})
}

func (f *fixture) change(t *testing.T, expected, version uint64, facets map[kernel.Facet]string) (map[string]kernel.StageKind, error) {
	t.Helper()
	var out map[string]kernel.StageKind
	err := f.commit(func(tx store.Tx) (err error) {
		out, err = f.repo.ApplySnapshotChange(tx, "b", expected, version, facets)
		return err
	})
	return out, err
}

func TestSnapshotChangeSendsEachMemberBackToTheEarliestStageWhoseFacetChanged(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	for i, id := range []string{"early", "mid", "late", "paused", "plain", "idle"} {
		if err := f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, "b", id, i) }); err != nil {
			t.Fatal(err)
		}
	}
	f.preflighted(t, "early", "mid", "late", "paused", "plain", "idle")
	if err := f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) }); err != nil {
		t.Fatal(err)
	}
	v1 := facetSet("d1", "s1", "r1", "u1", "y1")

	f.stage(t, "early", reads(v1)) // extraction finished
	f.stage(t, "mid", reads(v1))   // extraction
	f.stage(t, "mid", reads(v1))   // resolution finished
	for _, id := range []string{"late", "paused"} {
		for i := 0; i < 3; i++ { // extraction, resolution, export finished
			f.stage(t, id, reads(v1))
		}
	}
	pj := f.get(t, "paused")
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "paused", pj.Version, Event{Kind: EvPause})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// a stage begun with plain BeginStage records no reads
	f.complete(t, f.start(t, "plain"), sched.StageOutcome{Kind: sched.Completed})

	// only the render facet changes: export (render) is stale, earlier stages are not
	v2 := facetSet("d1", "s1", "r2", "u1", "y1")
	changed, err := f.change(t, 1, 2, v2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]kernel.StageKind{"late": kernel.StageExport, "paused": kernel.StageExport, "plain": kernel.StageExtractDetect}
	if len(changed) != len(want) {
		t.Fatalf("invalidated %v, want %v", changed, want)
	}
	for id, stage := range want {
		if changed[id] != stage {
			t.Fatalf("%s restarts at %q, want %q", id, changed[id], stage)
		}
	}
	for _, id := range []string{"early", "mid", "idle"} {
		if _, hit := changed[id]; hit {
			t.Fatalf("%s did not depend on the changed facet", id)
		}
	}
	if late := f.get(t, "late").Position; late.State != Queued || late.Cursor != kernel.StageExport {
		t.Fatalf("late: %+v", late)
	}
	// the user's pause is kept; resuming continues from the stale stage
	if p := f.get(t, "paused").Position; p.State != Paused || p.Resume != Queued || p.Cursor != kernel.StageExport {
		t.Fatalf("paused: %+v", p)
	}
	if v, _ := f.snapshotVersion(t); v != 2 {
		t.Fatalf("bound version %d", v)
	}

	// the dispose facet changes next: resolution (dispose) and everything after it
	v3 := facetSet("d1", "s2", "r2", "u1", "y1")
	changed, err = f.change(t, 2, 3, v3)
	if err != nil {
		t.Fatal(err)
	}
	if changed["mid"] != kernel.StageResolve || changed["late"] != kernel.StageResolve || changed["paused"] != kernel.StageResolve {
		t.Fatalf("second change: %v", changed)
	}
	if _, hit := changed["early"]; hit {
		t.Fatal("extraction does not read the dispose facet")
	}
}

func (f *fixture) snapshotVersion(t *testing.T) (uint64, error) {
	t.Helper()
	var v uint64
	err := f.commit(func(tx store.Tx) (err error) { v, err = f.repo.SnapshotVersion(tx, "b"); return err })
	return v, err
}

func TestSnapshotChangeIsAtomicAndVersionChecked(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	f.preflighted(t, "j")
	f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err })
	f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) })
	f.stage(t, "j", reads(facetSet("d1", "s1", "r1", "u1", "y1")))
	// an older or equal version, and a stale expected version, are refused without effect
	for _, c := range [][2]uint64{{1, 1}, {1, 0}, {0, 2}} {
		if _, err := f.change(t, c[0], c[1], facetSet("d2", "s1", "r1", "u1", "y1")); err == nil {
			t.Fatalf("change %v accepted", c)
		}
	}
	if v, _ := f.snapshotVersion(t); v != 1 {
		t.Fatalf("version moved to %d", v)
	}
	if st := f.get(t, "j").Position.State; st == Queued {
		t.Fatal("a refused change invalidated a member")
	}
	// a failure while invalidating rolls back the version binding as well
	boom := errors.New("boom")
	err := f.commit(func(tx store.Tx) error {
		if _, err := f.repo.ApplySnapshotChange(tx, "b", 1, 2, facetSet("d2", "s1", "r1", "u1", "y1")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if v, _ := f.snapshotVersion(t); v != 1 || f.get(t, "j").Position.State == Queued {
		t.Fatal("a rolled-back change left a partial effect")
	}
}

func TestSnapshotChangeDiscardsARunningAttemptThatReadOldFacets(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	f.member(t, "b", "j")
	f.preflighted(t, "j")
	f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err })
	f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) })
	attempt := f.startReading(t, "j", reads(facetSet("d1", "s1", "r1", "u1", "y1"))) // extraction running
	changed, err := f.change(t, 1, 2, facetSet("d2", "s1", "r1", "u1", "y1"))
	if err != nil || changed["j"] != kernel.StageExtractDetect {
		t.Fatalf("changed %v err %v", changed, err)
	}
	// the late result of the discarded attempt is rejected
	if err = f.commit(func(tx store.Tx) error {
		_, err := f.repo.CompleteStage(tx, attempt, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("late result of the old configuration: %v", err)
	}
	// a running attempt that already reads the new facets is left alone
	attempt = f.startReading(t, "j", reads(facetSet("d2", "s1", "r1", "u1", "y1")))
	changed, err = f.change(t, 2, 3, facetSet("d2", "s1", "r1", "u1", "y1"))
	if err != nil || len(changed) != 0 {
		t.Fatalf("an attempt on current facets was invalidated: %v %v", changed, err)
	}
	if err = f.commit(func(tx store.Tx) error {
		if err := f.ws.AdmitTx(tx, attempt); err != nil {
			return err
		}
		_, err := f.repo.CompleteStage(tx, attempt, sched.StageOutcome{Kind: sched.Completed})
		return err
	}); err != nil {
		t.Fatalf("current attempt rejected: %v", err)
	}
	// a running attempt begun without recorded reads is stale by default
	f2 := fixtureDB(t)
	f2.create(t, "b", RegularBatch)
	f2.member(t, "b", "j")
	f2.preflighted(t, "j")
	f2.commit(func(tx store.Tx) error { _, err := f2.repo.ConfirmBatch(tx, "b"); return err })
	f2.commit(func(tx store.Tx) error { return f2.repo.BindSnapshot(tx, "b", 0, 1) })
	f2.start(t, "j")
	if changed, err = f2.change(t, 1, 2, facetSet("d1", "s1", "r1", "u1", "y1")); err != nil || changed["j"] != kernel.StageExtractDetect {
		t.Fatalf("unrecorded attempt: %v %v", changed, err)
	}
}

func TestSnapshotChangeDoesNotTouchDraftsFinishedOrBlockedMembers(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	for i, id := range []string{"locked", "done"} {
		f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, "b", id, i) })
	}
	f.complete(t, f.start(t, "locked"), sched.StageOutcome{Kind: sched.Completed, Detail: DetailPasswordRequired})
	f.complete(t, f.start(t, "done"), sched.StageOutcome{Kind: sched.Completed})
	f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err })
	f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) })
	changed, err := f.change(t, 1, 2, facetSet("d2", "s2", "r2", "u2", "y2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, hit := changed["locked"]; hit {
		t.Fatal("a password block was bypassed by a configuration change")
	}
	if p := f.get(t, "locked").Position; p.State != Blocked || p.Block != BlockPassword {
		t.Fatalf("locked: %+v", p)
	}
}

func TestEveryLineageStageMapsToAStageATaskCanRestartAt(t *testing.T) {
	for stage := range lineage.Table {
		if stage == kernel.StagePublish {
			continue // a delivery is never invalidated, only superseded
		}
		if _, ok := stateOf[restartStage(stage)]; !ok {
			t.Fatalf("lineage stage %q has no restartable task state", stage)
		}
	}
	if restartStage(kernel.StageSupplementScan) != kernel.StageExtractDetect {
		t.Fatal("a stale supplementary scan must rerun from extraction")
	}
}

func TestSnapshotChangeLeavesCancelledMembersWithFinishedStagesAlone(t *testing.T) {
	f := fixtureDB(t)
	f.create(t, "b", RegularBatch)
	for i, id := range []string{"cancelled", "cancelling"} {
		f.commit(func(tx store.Tx) error { return f.repo.AddMember(tx, "b", id, i) })
	}
	f.preflighted(t, "cancelled", "cancelling")
	f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err })
	f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) })
	v1 := facetSet("d1", "s1", "r1", "u1", "y1")
	f.stage(t, "cancelled", v1x(v1))
	f.stage(t, "cancelling", v1x(v1))
	for _, id := range []string{"cancelled", "cancelling"} {
		job := f.get(t, id)
		if err := f.commit(func(tx store.Tx) error {
			_, err := f.repo.Apply(tx, id, job.Version, Event{Kind: EvCancel})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	job := f.get(t, "cancelled")
	if err := f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "cancelled", job.Version, Event{Kind: EvCancelDone})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if st := f.get(t, "cancelled").Position.State; st != Cancelled {
		t.Fatalf("setup: %s", st)
	}
	changed, err := f.change(t, 1, 2, facetSet("d2", "s2", "r2", "u2", "y2"))
	if err != nil || len(changed) != 0 {
		t.Fatalf("cancelled or cancelling members were touched: %v %v", changed, err)
	}
}

func v1x(f map[kernel.Facet]string) StageReads { return reads(f) }

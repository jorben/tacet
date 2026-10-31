//go:build darwin || windows

package jobs

import (
	"errors"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

func ckpt(id string) kernel.ArtifactRef {
	return kernel.ArtifactRef{Kind: "checkpoint", ID: id, Version: 1}
}

func (f *fixture) save(a kernel.AttemptRef, id string, seq uint64) error {
	return f.commit(func(tx store.Tx) error { return f.repo.SaveCheckpoint(tx, a, ckpt(id), seq) })
}

func (f *fixture) take(t *testing.T, job string, stage kernel.StageKind, r StageReads) (Checkpoint, bool) {
	t.Helper()
	var c Checkpoint
	var ok bool
	if err := f.commit(func(tx store.Tx) (err error) { c, ok, err = f.repo.TakeCheckpoint(tx, job, stage, r); return err }); err != nil {
		t.Fatal(err)
	}
	return c, ok
}

// running puts a job into a running extraction attempt reading v1.
func (f *fixture) running(t *testing.T, id string, v map[kernel.Facet]string) kernel.AttemptRef {
	t.Helper()
	f.create(t, "b", RegularBatch)
	f.member(t, "b", id)
	f.preflighted(t, id)
	f.commit(func(tx store.Tx) error { _, err := f.repo.ConfirmBatch(tx, "b"); return err })
	f.commit(func(tx store.Tx) error { return f.repo.BindSnapshot(tx, "b", 0, 1) })
	return f.startReading(t, id, reads(v))
}

func TestCheckpointSurvivesPauseAndRecoveryAndIsHandedToTheNextAttempt(t *testing.T) {
	f := fixtureDB(t)
	v := facetSet("d1", "s1", "r1", "u1", "y1")
	a := f.running(t, "j", v)
	if err := f.save(a, "c1", 10); err != nil {
		t.Fatal(err)
	}
	if err := f.save(a, "c2", 25); err != nil {
		t.Fatal(err)
	}
	// the user pauses: the attempt is abandoned, the checkpoint is kept
	job := f.get(t, "j")
	f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvPause})
		return err
	})
	if err := f.ws.Admit(a); err == nil {
		t.Fatal("abandoned attempt still admitted")
	}
	c, ok := f.take(t, "j", kernel.StageExtractDetect, reads(v))
	if !ok || c.Seq != 25 || c.Ref.ID != "c2" {
		t.Fatalf("checkpoint after pause: %+v %v", c, ok)
	}
	// a crash and restart (recovery) keeps it too
	job = f.get(t, "j")
	f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvResume})
		return err
	})
	b := f.startReading(t, "j", reads(v))
	f.commit(func(tx store.Tx) error { _, err := f.repo.RecoverMember(tx, "j"); return err })
	if c, ok = f.take(t, "j", kernel.StageExtractDetect, reads(v)); !ok || c.Seq != 25 {
		t.Fatalf("checkpoint after recovery: %+v %v", c, ok)
	}
	if err := f.save(b, "late", 99); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("a recovered (discarded) attempt saved a checkpoint: %v", err)
	}
	if c, _ = f.take(t, "j", kernel.StageExtractDetect, reads(v)); c.Seq != 25 {
		t.Fatal("a rejected save changed the checkpoint")
	}
}

func TestCheckpointSequenceMustAdvanceAndOnlyTheCurrentAttemptMaySave(t *testing.T) {
	f := fixtureDB(t)
	v := facetSet("d1", "s1", "r1", "u1", "y1")
	a := f.running(t, "j", v)
	if err := f.save(a, "c", 10); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{10, 9, 0} {
		if err := f.save(a, "c", seq); !errors.Is(err, ErrJobRecord) {
			t.Fatalf("seq %d accepted: %v", seq, err)
		}
	}
	if err := f.commit(func(tx store.Tx) error { return f.repo.SaveCheckpoint(tx, a, kernel.ArtifactRef{}, 11) }); !errors.Is(err, ErrJobRecord) {
		t.Fatalf("empty reference: %v", err)
	}
	foreign := a
	foreign.AttemptID = "a_other"
	if err := f.save(foreign, "x", 50); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("another attempt: %v", err)
	}
	old := a
	old.JobVersion = a.JobVersion + 1
	if err := f.save(old, "x", 50); !errors.Is(err, ErrLateOutcome) {
		t.Fatalf("another job version: %v", err)
	}
	if c, _ := f.take(t, "j", kernel.StageExtractDetect, reads(v)); c.Seq != 10 || c.Ref.ID != "c" {
		t.Fatalf("checkpoint changed by refused saves: %+v", c)
	}
}

func TestStaleCheckpointIsDiscardedWhenInputOrAFacetChanges(t *testing.T) {
	v := facetSet("d1", "s1", "r1", "u1", "y1")
	for name, now := range map[string]StageReads{
		"input changed":         {Input: "in2", Facets: v},
		"detect facet changed":  {Input: "in1", Facets: facetSet("d2", "s1", "r1", "u1", "y1")},
		"runtime facet changed": {Input: "in1", Facets: facetSet("d1", "s1", "r1", "u2", "y1")},
		"facets unknown":        {Input: "in1"},
	} {
		t.Run(name, func(t *testing.T) {
			f := fixtureDB(t)
			a := f.running(t, "j", v)
			f.save(a, "c", 10)
			if _, ok := f.take(t, "j", kernel.StageExtractDetect, now); ok {
				t.Fatal("a checkpoint of other input or configuration was handed out")
			}
			if _, ok := f.take(t, "j", kernel.StageExtractDetect, reads(v)); ok {
				t.Fatal("the stale checkpoint was kept for later")
			}
		})
	}
	// a facet the stage does not read does not invalidate it
	f := fixtureDB(t)
	a := f.running(t, "j", v)
	f.save(a, "c", 10)
	if _, ok := f.take(t, "j", kernel.StageExtractDetect, StageReads{Input: "in1", Facets: facetSet("d1", "s9", "r9", "u1", "y9")}); !ok {
		t.Fatal("a facet extraction does not read discarded its checkpoint")
	}
}

func TestCheckpointIsClearedByCompletionAndCancellation(t *testing.T) {
	v := facetSet("d1", "s1", "r1", "u1", "y1")
	f := fixtureDB(t)
	a := f.running(t, "j", v)
	f.save(a, "c", 10)
	f.complete(t, a, sched.StageOutcome{Kind: sched.Completed})
	if _, ok := f.take(t, "j", kernel.StageExtractDetect, reads(v)); ok {
		t.Fatal("checkpoint kept after the stage completed")
	}

	f = fixtureDB(t)
	a = f.running(t, "j", v)
	f.save(a, "c", 10)
	job := f.get(t, "j")
	f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvCancel})
		return err
	})
	job = f.get(t, "j")
	f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvCancelDone})
		return err
	})
	if _, ok := f.take(t, "j", kernel.StageExtractDetect, reads(v)); ok {
		t.Fatal("checkpoint of a cancelled task kept")
	}

	// recovery of a task that was cancelling completes the cancellation and clears it
	f = fixtureDB(t)
	a = f.running(t, "j", v)
	f.save(a, "c", 10)
	job = f.get(t, "j")
	f.commit(func(tx store.Tx) error {
		_, err := f.repo.Apply(tx, "j", job.Version, Event{Kind: EvCancel})
		return err
	})
	f.commit(func(tx store.Tx) error { _, err := f.repo.RecoverMember(tx, "j"); return err })
	if got := f.get(t, "j").Position.State; got != Cancelled {
		t.Fatalf("state %s", got)
	}
	if _, ok := f.take(t, "j", kernel.StageExtractDetect, reads(v)); ok {
		t.Fatal("checkpoint kept after recovering a cancelling task")
	}
}

func TestCheckpointsAreKeptPerStageAndJob(t *testing.T) {
	v := facetSet("d1", "s1", "r1", "u1", "y1")
	f := fixtureDB(t)
	a := f.running(t, "j", v)
	f.save(a, "c", 10)
	if _, ok := f.take(t, "j", kernel.StageExport, reads(v)); ok {
		t.Fatal("an extraction checkpoint was offered to export")
	}
	if _, ok := f.take(t, "other", kernel.StageExtractDetect, reads(v)); ok {
		t.Fatal("another job's checkpoint was offered")
	}
}

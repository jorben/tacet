//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
)

// holdStage keeps the stage waiting at its start, whatever its context says, as a stage does that takes its time
// to stop. only tells which files wait; held counts those that came.
func holdStage(t *testing.T, e *Engine, stage kernel.StageKind, only func(job string) bool) (held *atomic.Int32, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once atomic.Bool
	release = func() {
		if once.CompareAndSwap(false, true) {
			close(gate)
		}
	}
	t.Cleanup(release) // before the engine closes (cleanups run last first)
	held = &atomic.Int32{}
	e.stageStarted = func(job string, s kernel.StageKind) {
		if s == stage && (only == nil || only(job)) {
			held.Add(1)
			<-gate
		}
	}
	return held, release
}

func (r *parkRig) state(batch string, file int) string {
	r.t.Helper()
	st, err := r.e.Status(r.ctx, batch, 10)
	if err != nil {
		r.t.Fatal(err)
	}
	return st.Files[file].State
}

func (r *parkRig) unfinished() int {
	r.t.Helper()
	list, err := r.e.Unfinished(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	return len(list)
}

// stillStopping is what a cancelled file with a stage that has not stopped reads as. It is the precondition of the
// tests below: without it they would not tell a settled file from one that never had to wait.
func (r *parkRig) stillStopping(batch string) {
	r.t.Helper()
	if s := r.state(batch, 0); s != "cancelling" {
		r.t.Fatalf("while its stage has not stopped the file is %s", s)
	}
	if err := r.e.Clean(r.ctx, batch); !errors.Is(err, ErrNotAllowed) {
		r.t.Fatalf("cleaning while a stage runs: %v", err)
	}
	if n := r.unfinished(); n != 1 {
		r.t.Fatalf("%d batches are listed while a stage runs", n)
	}
}

// A stage told to stop reports back as a late result. In each stage that is enough: nobody cancels again, and no
// restart comes, before the file is cancelled, its batch finished, and Clean and Wait go through.
func TestACancelledFileEndsWhenItsStageStops(t *testing.T) {
	for _, stage := range []kernel.StageKind{kernel.StagePreflight, kernel.StageExtractDetect, kernel.StageResolve, kernel.StageExport, kernel.StageValidate, kernel.StagePublish} {
		t.Run(string(stage), func(t *testing.T) {
			r := newParkRig(t)
			held, release := holdStage(t, r.e, stage, nil)
			st, err := r.imp("batch", r.file(noReview))
			if err != nil {
				t.Fatal(err)
			}
			if stage != kernel.StagePreflight {
				if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
					t.Fatal(err)
				}
			}
			eventually(t, "at the stage", func() bool { return held.Load() == 1 })
			if err = r.e.Cancel(r.ctx, st.ID); err != nil {
				t.Fatal(err)
			}
			r.stillStopping(st.ID)

			// the screens learn it from an event: nothing else tells them the stage has stopped
			var told atomic.Int32
			defer r.e.Subscribe(func(b string) {
				if b == st.ID {
					told.Add(1)
				}
			})()
			release()
			eventually(t, "cancelled", func() bool { return r.state(st.ID, 0) == "cancelled" })
			eventually(t, "told", func() bool { return told.Load() > 0 })
			if n := r.unfinished(); n != 0 {
				t.Fatalf("%d batches are still listed", n)
			}
			if err = r.e.Clean(r.ctx, st.ID); err != nil {
				t.Fatal(err)
			}
			if res, err := r.e.Wait(r.ctx, st.ID); err != nil || res.Files[0].State != "cancelled" {
				t.Fatalf("wait: %+v %v", res, err)
			}
			if kept, _ := os.ReadDir(filepath.Join(r.ws, "resources")); len(kept) != 0 {
				t.Fatalf("clean left %d resource(s)", len(kept))
			}
			if s := r.e.queue.State(); s.Running != 0 || s.Pending != 0 {
				t.Fatalf("the scheduler still holds %+v", s)
			}
			// and it stays so across a restart
			if err = r.e.Close(); err != nil {
				t.Fatal(err)
			}
			r.e = openEngine(t, r.ws, r.key, false)
			if n := r.unfinished(); n != 0 {
				t.Fatalf("%d batches are listed after a restart", n)
			}
		})
	}
}

// The batch ends with the last of its files, not with the first.
func TestABatchEndsWhenItsLastFileHasStopped(t *testing.T) {
	r := newParkRig(t)
	var first atomic.Value
	held, release := holdStage(t, r.e, kernel.StagePreflight, func(job string) bool {
		first.CompareAndSwap(nil, job)
		return first.Load() == job
	})
	st, err := r.imp("batch", r.file(noReview), r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 })
	if err = r.e.Cancel(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	stopping, other := 0, 1
	if st.Files[1].ID == first.Load() {
		stopping, other = 1, 0
	}
	eventually(t, "cancelled, the other file", func() bool { return r.state(st.ID, other) == "cancelled" })
	if s := r.state(st.ID, stopping); s != "cancelling" {
		t.Fatalf("the file with the stage is %s", s)
	}
	if err = r.e.Clean(r.ctx, st.ID); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("cleaning with one file stopping: %v", err)
	}
	if n := r.unfinished(); n != 1 {
		t.Fatalf("%d batches are listed with one file stopping", n)
	}
	release()
	eventually(t, "finished", func() bool { return r.unfinished() == 0 })
	if err = r.e.Clean(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
}

// A stage still waiting in the queue is taken out: nothing is left to report back, so the file ends at once.
func TestCancellingAQueuedStageEndsTheFileAtOnce(t *testing.T) {
	r := newParkRig(t)
	var ran atomic.Int32
	r.e.stageStarted = func(string, kernel.StageKind) { ran.Add(1) }
	r.e.queue.PauseDispatch(true)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "queued", func() bool { return r.e.queue.State().Pending == 1 })
	if err = r.e.Cancel(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "cancelled" {
		t.Fatalf("the file is %s", s)
	}
	if s := r.e.queue.State(); s.Pending != 0 {
		t.Fatalf("still queued: %+v", s)
	}
	if err = r.e.Clean(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	r.e.queue.PauseDispatch(false)
	if n := ran.Load(); n != 0 {
		t.Fatalf("%d stage(s) ran", n)
	}
}

// An attempt on record that is not queued yet can be neither found nor removed by the scheduler. The cancel waits
// for it to arrive, so nothing is left to run for a file already called cancelled, and it finishes what it began
// even if its caller is gone by then.
func TestACancelFindsAStageOnItsWayToTheQueue(t *testing.T) {
	for name, interrupted := range map[string]bool{"caller stays": false, "caller gone": true} {
		t.Run(name, func(t *testing.T) {
			r := newParkRig(t)
			r.e.queue.PauseDispatch(true) // what reaches the queue stays there for this test to see
			var waiting atomic.Int32
			gate := make(chan struct{})
			r.e.beforeSubmit = func() { waiting.Add(1); <-gate }
			st, err := r.imp("batch", r.file(noReview))
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, "begun", func() bool { return waiting.Load() == 1 })
			ctx, leave := context.WithCancel(r.ctx)
			defer leave()
			done := make(chan error, 1)
			go func() { done <- r.e.Cancel(ctx, st.ID) }()
			eventually(t, "cancelling", func() bool { s := r.state(st.ID, 0); return s == "cancelling" || s == "cancelled" })
			if interrupted {
				leave()
			}
			close(gate)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			eventually(t, "handed over", func() bool { _, on := r.e.nextStage.Load(st.Files[0].ID); return !on })
			if s := r.e.queue.State(); s.Pending != 0 {
				t.Fatalf("a stage was left queued for the cancelled file: %+v", s)
			}
			if s := r.state(st.ID, 0); s != "cancelled" {
				t.Fatalf("the file is %s", s)
			}
			if err = r.e.Clean(r.ctx, st.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A file can end between the commit of a cancel and the moment it looks at its files again. That file is over; the
// others still end with this cancel.
func TestACancelGoesOnPastAFileThatEndedMeanwhile(t *testing.T) {
	r := newParkRig(t)
	held, release := holdStage(t, r.e, kernel.StagePreflight, nil)
	var submits atomic.Int32
	gate := make(chan struct{})
	r.e.beforeSubmit = func() { // one file reaches its stage, the other keeps the cancel waiting
		if submits.Add(1) == 2 {
			<-gate
		}
	}
	st, err := r.imp("batch", r.file(noReview), r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 && submits.Load() == 2 })
	done := make(chan error, 1)
	go func() { done <- r.e.Cancel(r.ctx, st.ID) }()
	cancelled := func() (n int) {
		for i := range st.Files {
			if r.state(st.ID, i) == "cancelled" {
				n++
			}
		}
		return n
	}
	eventually(t, "cancelling", func() bool { return r.state(st.ID, 0) != "preflighting" && r.state(st.ID, 1) != "preflighting" })
	release() // its stage ends and settles its file, while the cancel still waits
	eventually(t, "cancelled, the file that was at its stage", func() bool { return cancelled() == 1 })
	close(gate)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	// the scheduler may have taken the other stage before the cancel reached it: then that one ends a moment later
	eventually(t, "finished", func() bool { return cancelled() == 2 && r.unfinished() == 0 })
}

// A pause lets go of the attempt while its stage still runs. Cancelling then is not over before that stage is:
// Clean would remove what it is working on.
func TestAPausedFileWithItsStageStillRunningIsCancelledWhenThatStops(t *testing.T) {
	r := newParkRig(t)
	held, release := holdStage(t, r.e, kernel.StagePreflight, nil)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 })
	if err = r.e.Pause(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "paused" {
		t.Fatalf("the file is %s", s)
	}
	if err = r.e.Cancel(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	r.stillStopping(st.ID)
	release()
	eventually(t, "cancelled", func() bool { return r.state(st.ID, 0) == "cancelled" })
	if err = r.e.Clean(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
}

// The old stage of a file that is only paused ends without touching it.
func TestAnOldStageEndingLeavesAPausedFileAsItIs(t *testing.T) {
	r := newParkRig(t)
	held, release := holdStage(t, r.e, kernel.StagePreflight, nil)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 })
	if err = r.e.Pause(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	release()
	eventually(t, "stopped", func() bool { return r.e.queue.State().Running == 0 && !r.e.disp.Running(st.Files[0].ID) })
	if s := r.state(st.ID, 0); s != "paused" {
		t.Fatalf("the file is %s", s)
	}
	if n := r.unfinished(); n != 1 {
		t.Fatalf("%d batches are listed", n)
	}
}

// Asking again does not call the file cancelled before its stage has stopped, as it used to.
func TestASecondCancelDoesNotEndAFileWhoseStageStillRuns(t *testing.T) {
	r := newParkRig(t)
	held, release := holdStage(t, r.e, kernel.StagePreflight, nil)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 })
	for range 2 {
		if err = r.e.Cancel(r.ctx, st.ID); err != nil {
			t.Fatal(err)
		}
	}
	r.stillStopping(st.ID)
	release()
	eventually(t, "cancelled", func() bool { return r.state(st.ID, 0) == "cancelled" })
}

// The start a call left on record goes with the cancel: nothing has to carry it out for it to be dropped, and a wait
// for the batch ends.
func TestACancelDropsTheStartTheDraftKept(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.e.Preflight(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	ended, end := context.WithCancel(r.ctx)
	end() // the call ends, the request stays on record
	if err = r.e.Start(ended, StartRequest{Batch: st.ID, Authorize: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !r.e.startPending(r.ctx, st.ID) {
		t.Fatal("the precondition: the start is on record")
	}
	if err = r.e.Cancel(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if r.e.startPending(r.ctx, st.ID) {
		t.Fatal("the start is still on record after the cancel")
	}
	if res, err := r.e.Wait(r.ctx, st.ID); err != nil || res.Started || res.Files[0].State != "cancelled" {
		t.Fatalf("wait: %+v %v", res, err)
	}
}

// A file that has ended stays what it is; the cancel of its batch is not refused for it.
func TestACancelLeavesAFinishedFileAsItIs(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("batch", noReview, "ready_to_publish")
	if err := r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, r.e, st.ID)
	if err := r.e.Cancel(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "completed" {
		t.Fatalf("the file is %s", s)
	}
}

// There is nothing to stop in a batch that is not there, which is not a failure: clean --discard goes on to say so.
func TestCancellingABatchThatIsNotThereChangesNothing(t *testing.T) {
	r := newParkRig(t)
	if err := r.e.Cancel(r.ctx, "b9999"); err != nil {
		t.Fatal(err)
	}
}

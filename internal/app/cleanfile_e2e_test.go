//go:build darwin

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
)

// twoInReview is a started batch of two files that both wait for review, not authorized to publish.
func (r *parkRig) twoInReview() BatchStatus {
	r.t.Helper()
	st, err := r.imp("batch", r.file(needsReview), r.file(needsReview))
	if err != nil {
		r.t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); err != nil {
		r.t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		r.t.Fatal(err)
	}
	for _, f := range st.Files {
		if f.State != "awaiting_review" {
			r.t.Fatalf("%+v", st.Files)
		}
	}
	return st
}

// decisions are what the kernel holds on a file, by finding.
func (r *parkRig) decisions(job string) map[int64]string {
	r.t.Helper()
	fs, err := r.e.Findings(r.ctx, job, 0, 50)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[int64]string{}
	for _, f := range fs {
		out[f.ID] = f.Decision
	}
	return out
}

// #41: cleaning one file takes what that file holds and nothing else. The file beside it keeps its data, its key and
// the choice made on it through a restart, and goes on to be exported; another batch is not touched at all.
func TestCleaningOneFileLeavesTheRestOfItsBatchToGoOn(t *testing.T) {
	r := newParkRig(t)
	other := r.waiting("batch", needsReview, "awaiting_review")
	st := r.twoInReview()
	gone, kept := st.Files[0].ID, st.Files[1].ID
	fs, err := r.e.Findings(r.ctx, kept, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatalf("%+v %v", fs, err)
	}
	ds := []FindingDecision{{ID: fs[0].ID, Keep: true}}
	for _, f := range fs[1:] {
		ds = append(ds, FindingDecision{ID: f.ID})
	}
	if err = r.e.DecideFindings(r.ctx, kept, ds); err != nil {
		t.Fatal(err)
	}
	if _, err = r.e.Wait(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if r.decisions(kept)[fs[0].ID] != "keep" { // the check after the restart below would pass on any file without this
		t.Fatalf("the choice was not stored: %v", r.decisions(kept))
	}

	if err = r.e.CancelFile(r.ctx, gone); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "cancelled" {
		t.Fatalf("the file cancelled is %s", s)
	}
	if err = r.e.CleanFile(r.ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, err = r.e.ws.Keys().Get(keys.Task, gone); err == nil {
		t.Fatal("the key of the cleaned file is still there")
	}
	if units, err := r.e.Document(r.ctx, gone, 0, 100); err == nil && len(units) > 0 {
		t.Fatalf("the document of the cleaned file is still there: %+v", units)
	}
	if err = r.e.CleanFile(r.ctx, gone); err != nil { // the person may be told twice, and a restart may ask again
		t.Fatalf("cleaning it again: %v", err)
	}

	before, err := r.e.Status(r.ctx, other.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if before.Files[0].State != "awaiting_review" {
		t.Fatalf("the other batch: %+v", before.Files)
	}
	if list, err := r.e.Unfinished(r.ctx); err != nil || len(list) != 2 {
		t.Fatalf("what is listed after cleaning a file: %+v %v", list, err)
	}

	if err = r.e.Close(); err != nil {
		t.Fatalf("closing with a cleaned file in a batch: %v", err)
	}
	r.e = openEngine(t, r.ws, r.key, false)
	list, err := r.e.Unfinished(r.ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("after a restart: %+v %v", list, err)
	}
	if s := r.state(st.ID, 0); s != "cancelled" {
		t.Fatalf("after a restart the cleaned file is %s", s)
	}
	if _, err = r.e.ws.Keys().Get(keys.Task, kept); err != nil {
		t.Fatalf("after a restart the key of the other file: %v", err)
	}
	if got := r.decisions(kept)[fs[0].ID]; got != "keep" {
		t.Fatalf("after a restart the choice on the other file is %q", got)
	}
	if _, err = r.e.ws.Keys().Get(keys.Task, other.Files[0].ID); err != nil {
		t.Fatalf("after a restart the key of the other batch: %v", err)
	}

	// what the batch was left with can still be finished: the decided file goes out, the cleaned one does not
	if err = r.e.Resume(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, st.ID, false, r.out); err != nil {
		t.Fatal(err)
	}
	end, err := r.e.Wait(r.ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if end.Files[0].State != "cancelled" {
		t.Fatalf("the cleaned file after the export: %+v", end.Files[0])
	}
	remains := end.Files[1]
	if remains.State != "completed" && remains.State != "partial" {
		t.Fatalf("the file that was kept ended as %+v", remains)
	}
	if _, err = os.Stat(filepath.Join(r.out, remains.Output)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(r.out)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range entries {
		if strings.Contains(d.Name(), end.Files[0].Name) {
			t.Fatalf("the cleaned file was delivered: %s", d.Name())
		}
	}
	if list, err = r.e.Unfinished(r.ctx); err != nil || len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("after the export, what is left: %+v %v", list, err)
	}
	if err = r.e.Clean(r.ctx, st.ID); err != nil { // the batch as a whole, with a file already cleaned
		t.Fatalf("cleaning the finished batch: %v", err)
	}
}

// The last open file cancelled ends the batch, as with Skip: nothing is left to continue, so it is not listed again.
func TestCancellingTheLastOpenFileEndsItsBatch(t *testing.T) {
	r := newParkRig(t)
	st := r.twoInReview()
	for i, f := range st.Files {
		if n := r.unfinished(); n != 1 {
			t.Fatalf("before file %d: %d listed", i, n)
		}
		if err := r.e.CancelFile(r.ctx, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.unfinished(); n != 0 {
		t.Fatalf("%d listed with every file cancelled", n)
	}
}

// A batch left with nothing to start says so in the words a person is told, not as a broken record.
func TestStartingABatchWhoseFilesWereAllCancelledIsNotReady(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", r.file(noReview), r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Preflight(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	for _, f := range st.Files {
		if err = r.e.CancelFile(r.ctx, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("starting with every file cancelled: %v", err)
	}
}

// A file whose stage has not stopped cannot be cleaned, and the stage's end is what finishes it; the file beside it
// goes on.
func TestCleaningAFileWaitsForItsStageAndTheFileBesideItGoesOn(t *testing.T) {
	r := newParkRig(t)
	var first atomic.Value
	held, release := holdStage(t, r.e, kernel.StagePreflight, func(job string) bool {
		first.CompareAndSwap(nil, job)
		return first.Load() == job
	})
	st, err := r.imp("batch", r.file(needsReview), r.file(needsReview))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the stage", func() bool { return held.Load() == 1 })
	job := first.Load().(string)
	stopping, beside := 0, 1
	if st.Files[1].ID == job {
		stopping, beside = 1, 0
	}
	if err = r.e.CancelFile(r.ctx, job); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, stopping); s != "cancelling" {
		t.Fatalf("while its stage has not stopped the file is %s", s)
	}
	if err = r.e.CleanFile(r.ctx, job); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("cleaning a file still stopping: %v", err)
	}
	if _, err = r.e.ws.Keys().Get(keys.Task, job); err != nil {
		t.Fatalf("the refusal took its key: %v", err)
	}
	release()
	eventually(t, "cancelled", func() bool { return r.state(st.ID, stopping) == "cancelled" })
	if err = r.e.CleanFile(r.ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatalf("starting what is left: %v", err)
	}
	end, err := r.e.Wait(r.ctx, st.ID)
	if err != nil || end.Files[beside].State != "awaiting_review" {
		t.Fatalf("the file beside it: %+v %v", end, err)
	}
}

// A file that has ended stays as it ended, and what was exported stays where it went.
func TestCancellingAFileThatEndedChangesNothingAndAnUnknownFileIsNotFound(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "completed" {
		t.Fatalf("%+v %v", st, err)
	}
	job := st.Files[0].ID
	if err = r.e.CancelFile(r.ctx, job); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "completed" {
		t.Fatalf("a finished file cancelled is %s", s)
	}
	if err = r.e.CleanFile(r.ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(r.out, st.Files[0].Output)); err != nil {
		t.Fatal(err)
	}
	if err = r.e.CancelFile(r.ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelling an unknown file: %v", err)
	}
	if err = r.e.CleanFile(r.ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleaning an unknown file: %v", err)
	}
}

// Only a file at rest can be cleaned: one that waits for a person has not ended, and Clean's rule holds for it too.
func TestCleaningAFileThatWaitsIsRefused(t *testing.T) {
	r := newParkRig(t)
	st := r.twoInReview()
	if err := r.e.CleanFile(r.ctx, st.Files[0].ID); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("cleaning a file that waits: %v", err)
	}
	if _, err := r.e.ws.Keys().Get(keys.Task, st.Files[0].ID); err != nil {
		t.Fatalf("the refusal took its key: %v", err)
	}
}

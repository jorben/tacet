//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// parkRig is an engine with one workspace, for the rules of a task that waits for a person.
type parkRig struct {
	t            *testing.T
	ws, in, out  string
	key          *memKey
	e            *Engine
	ctx          context.Context
	fileSequence int
}

func newParkRig(t *testing.T) *parkRig {
	t.Helper()
	r := &parkRig{t: t, ws: filepath.Join(t.TempDir(), "ws"), in: t.TempDir(), out: t.TempDir(), key: &memKey{}, ctx: t.Context()}
	r.e = openEngine(t, r.ws, r.key, true)
	t.Cleanup(func() { r.e.Close() })
	return r
}

func (r *parkRig) file(body string) string {
	r.t.Helper()
	r.fileSequence++
	p := filepath.Join(r.in, "f"+string(rune('a'+r.fileSequence))+".txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

const (
	needsReview = "证件 110101199003074519 与 110101199003074518\n"
	noReview    = "电话 13812345678\n"
)

func (r *parkRig) imp(kind string, park bool, files ...string) (BatchStatus, error) {
	return r.e.Import(r.ctx, ImportRequest{Kind: kind, Files: files, OutputDir: r.out, ParkWaiting: park})
}

// waiting takes a task to where it waits for a person; it is not authorized to publish.
func (r *parkRig) waiting(kind, body, wantState string) BatchStatus {
	r.t.Helper()
	st, err := r.imp(kind, true, r.file(body))
	if err != nil {
		r.t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); err != nil {
		r.t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		r.t.Fatal(err)
	}
	if st.Files[0].State != wantState {
		r.t.Fatalf("state %+v, want %s", st.Files[0], wantState)
	}
	return st
}

// A1, A2: a waiting single task makes room; everything about it stays.
func TestAWaitingTaskIsParkedByTheNextImportOfItsKind(t *testing.T) {
	for _, tc := range []struct{ name, kind, body, state string }{
		{"awaiting review", "quick", needsReview, "awaiting_review"},
		{"ready, not authorized", "quick", noReview, "ready_to_publish"},
		{"a batch awaiting review", "batch", needsReview, "awaiting_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newParkRig(t)
			old := r.waiting(tc.kind, tc.body, tc.state)
			before, err := r.e.Findings(r.ctx, old.Files[0].ID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			told := map[string]bool{}
			var mu sync.Mutex
			defer r.e.Subscribe(func(b string) { mu.Lock(); told[b] = true; mu.Unlock() })()
			st, err := r.imp(tc.kind, true, r.file(noReview))
			if err != nil {
				t.Fatalf("the next import: %v", err)
			}
			mu.Lock()
			if !told[old.ID] { // a window showing it reads it again
				t.Errorf("no change was announced for the parked task: %v", told)
			}
			mu.Unlock()
			if st.Displaced != old.ID || st.ID == old.ID {
				t.Fatalf("displaced %q by %q, want %q", st.Displaced, st.ID, old.ID)
			}
			list, err := r.e.Unfinished(r.ctx)
			if err != nil || len(list) != 2 {
				t.Fatalf("unfinished %+v %v", list, err)
			}
			kept, err := r.e.Status(r.ctx, old.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if !kept.Parked || kept.Files[0].State != tc.state || kept.Files[0].Pending != old.Files[0].Pending {
				t.Fatalf("the parked task changed: %+v", kept)
			}
			after, err := r.e.Findings(r.ctx, old.Files[0].ID, 0, 50)
			if err != nil || len(after) != len(before) {
				t.Fatalf("findings %d -> %d %v", len(before), len(after), err)
			}
			if now, _ := r.e.Status(r.ctx, st.ID, 10); now.Parked {
				t.Fatalf("the new task is parked: %+v", now)
			}
		})
	}
}

// Without the request nothing is parked (A7): this is what an older caller gets.
func TestWithoutParkWaitingAWaitingTaskStillRefuses(t *testing.T) {
	r := newParkRig(t)
	old := r.waiting("quick", needsReview, "awaiting_review")
	if _, err := r.imp("quick", false, r.file(noReview)); err == nil || err.Error() != CodeQuickTaskActive {
		t.Fatalf("import: %v", err)
	}
	if st, _ := r.e.Status(r.ctx, old.ID, 10); st.Parked {
		t.Fatal("parked without being asked to")
	}
}

// A3 with the real pipeline, and A5: paused is waiting for a person; a stage running or queued is not.
func TestABatchIsParkedOnceItIsPausedAndNotBefore(t *testing.T) {
	r := newParkRig(t)
	inspect, extract := make(chan struct{}), make(chan struct{})
	r.e.beforeDispatch = func(stage kernel.StageKind) {
		switch stage {
		case kernel.StagePreflight:
			<-inspect
		case kernel.StageExtractDetect:
			<-extract
		}
	}
	t.Cleanup(func() { close(extract) }) // before the engine closes (cleanups run last first)
	st, err := r.imp("batch", true, r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	// its file is being inspected and it was never confirmed: a draft, with its slot
	if _, err = r.imp("batch", true, r.file(noReview)); err == nil || err.Error() != CodeBatchActive {
		t.Fatalf("while inspecting: %v", err)
	}
	close(inspect)
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	// confirmed and queued for its next stage: work under way, the slot stays
	now, _ := r.e.Status(r.ctx, st.ID, 10)
	if now.Files[0].State != "queued" {
		t.Fatalf("not held in the queue: %+v", now.Files[0])
	}
	if _, err = r.imp("batch", true, r.file(noReview)); err == nil || err.Error() != CodeBatchActive {
		t.Fatalf("while queued: %v", err)
	}
	if err = r.e.Pause(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if now, _ = r.e.Status(r.ctx, st.ID, 10); now.Files[0].State != "paused" {
		t.Fatalf("not paused: %+v", now.Files[0])
	}
	next, err := r.imp("batch", true, r.file(noReview))
	if err != nil {
		t.Fatalf("after pausing: %v", err)
	}
	if next.Displaced != st.ID {
		t.Fatalf("displaced %q", next.Displaced)
	}
}

// A file that goes back to work takes its batch out of waiting (mixed batches: internal/jobs).
func TestABatchWhoseFileWentBackToWorkKeepsTheSlot(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("batch", needsReview, "awaiting_review")
	job := st.Files[0].ID
	// a decision sends the file back to work: until it waits again the batch holds its slot
	r.e.beforeDispatch = func(kernel.StageKind) { time.Sleep(300 * time.Millisecond) }
	fs, err := r.e.Findings(r.ctx, job, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatal(fs, err)
	}
	if err = r.e.DecideFinding(r.ctx, job, fs[0].ID, false); err != nil {
		t.Fatal(err)
	}
	now, _ := r.e.Status(r.ctx, st.ID, 10)
	if s := now.Files[0].State; s == "awaiting_review" || s == "ready_to_publish" {
		t.Fatalf("the file did not go back to work: %s", s)
	}
	if _, err = r.imp("batch", true, r.file(noReview)); err == nil || err.Error() != CodeBatchActive {
		t.Fatalf("while it works: %v", err)
	}
	if now, _ = r.e.Status(r.ctx, st.ID, 10); now.Parked {
		t.Fatal("parked while working")
	}
}

// A publish that is authorized moves on by itself, so its file is not waiting for anyone.
func TestAFileReadyToPublishByItselfIsNotWaiting(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", true, r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("%+v %v", st, err)
	}
	// granted, but the publish has not been dispatched: what the hook holds open
	if err = r.e.ws.Commit(r.ctx, func(tx store.Tx) error {
		run, err := getRun(tx, st.ID)
		if err != nil {
			return err
		}
		run.Grant = true
		return putRun(tx, run)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.imp("batch", true, r.file(noReview)); err == nil || err.Error() != CodeBatchActive {
		t.Fatalf("an authorized file: %v", err)
	}
}

// A6: a draft whose start was refused keeps its slot. Its files were not committed to a run.
func TestARefusedDraftIsNotParked(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", true, r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Preset: "no-such-preset"}); err == nil {
		t.Fatal("started with an unknown preset")
	}
	if _, err = r.imp("batch", true, r.file(noReview)); err == nil || err.Error() != CodeBatchActive {
		t.Fatalf("over a draft: %v", err)
	}
	if now, _ := r.e.Status(r.ctx, st.ID, 10); now.Parked {
		t.Fatal("a draft was parked")
	}
}

// A10: parking and the new draft are one transaction.
func TestAnImportThatFailsLeavesTheHolderHoldingTheSlot(t *testing.T) {
	r := newParkRig(t)
	old := r.waiting("quick", needsReview, "awaiting_review")
	boom := errors.New("DRAFT_FAILED")
	calls := 0
	r.e.beforeDraft = func() error { calls++; return boom }
	if _, err := r.imp("quick", true, r.file(noReview)); !errors.Is(err, boom) {
		t.Fatalf("import: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the failure ran %d times", calls)
	}
	r.e.beforeDraft = nil
	now, err := r.e.Status(r.ctx, old.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if now.Parked {
		t.Fatal("the park survived the failed import")
	}
	if list, _ := r.e.Unfinished(r.ctx); len(list) != 1 {
		t.Fatalf("the failed import left a batch: %+v", list)
	}
	// the holder still holds: without the request the next import is refused
	if _, err = r.imp("quick", false, r.file(noReview)); err == nil || err.Error() != CodeQuickTaskActive {
		t.Fatalf("the slot: %v", err)
	}
}

// What Import leaves when it stops after its first transaction (the keys, the members, an interrupt): the holder stays
// parked and can be continued, and the empty draft does not keep the next import out.
func TestAnImportThatStopsBeforeItsFilesDoesNotKeepTheSlot(t *testing.T) {
	r := newParkRig(t)
	old := r.waiting("quick", needsReview, "awaiting_review")
	boom := errors.New("KEYS_FAILED")
	r.e.beforeMembers = func() error { return boom }
	if _, err := r.imp("quick", true, r.file(noReview)); !errors.Is(err, boom) {
		t.Fatalf("import: %v", err)
	}
	r.e.beforeMembers = nil
	now, err := r.e.Status(r.ctx, old.ID, 10)
	if err != nil || !now.Parked || now.Files[0].State != "awaiting_review" {
		t.Fatalf("the holder after the failed import: %+v %v", now, err)
	}
	next, err := r.imp("quick", true, r.file(noReview))
	if err != nil {
		t.Fatalf("the next import was kept out by the empty draft: %v", err)
	}
	if next.Displaced != "" {
		t.Fatalf("the empty draft was reported as parked: %q", next.Displaced)
	}
	list, err := r.e.Unfinished(r.ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("listed %+v %v, want the holder and the next one", list, err)
	}
}

// A task blocked at publishing, continued after a restart: its document is there to review, and what it publishes
// on the retry is what was decided since, not what was built before.
func TestAChoiceMadeOnABlockedTaskIsWhatItsRetryPublishes(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("quick", noReview, "ready_to_publish")
	job := st.Files[0].ID
	finding := func() int64 {
		t.Helper()
		units, err := r.e.Document(r.ctx, job, 0, 100)
		if err != nil || len(units) == 0 || len(units[0].Findings) == 0 {
			t.Fatalf("the document of the task: %+v %v", units, err)
		}
		return units[0].Findings[0].ID
	}
	decide := func(keep bool) {
		t.Helper()
		if err := r.e.DecideFindings(r.ctx, job, []FindingDecision{{ID: finding(), Keep: keep}}); err != nil {
			t.Fatal(err)
		}
	}
	// the number stays in what was built first, then the name is taken: the file blocks with that candidate
	decide(true)
	var err error
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("after keeping: %+v %v", st, err)
	}
	taken := filepath.Join(r.out, st.ID+"-"+st.Files[0].Name+".txt")
	if err = os.WriteFile(taken, []byte("somebody else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "blocked" || st.Files[0].Detail != "PUBLISH_NAME_CONFLICT" {
		t.Fatalf("with the name taken: %+v %v", st, err)
	}
	r.e.Close()
	r.e = openEngine(t, r.ws, r.key, true)
	// a restart leaves every file paused; “继续” resumes it, and only then does it say what it waits for
	if err = r.e.Resume(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Status(r.ctx, st.ID, 10); err != nil || st.Files[0].State != "blocked" || st.Files[0].Detail != "PUBLISH_NAME_CONFLICT" {
		t.Fatalf("after the restart: %+v %v", st, err)
	}
	decide(false)
	if st, err = r.e.Status(r.ctx, st.ID, 10); err != nil || st.Files[0].State != "blocked" {
		t.Fatalf("a choice released the file: %+v %v", st, err)
	}
	if err = os.Remove(taken); err != nil {
		t.Fatal(err)
	}
	// exporting again, as the screen does: the decision took back the authorization of the first export
	if err = r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Retry(r.ctx, st.ID, []string{job}); err != nil {
		t.Fatal(err)
	}
	st = waitCompleted(t, r.e, st.ID)
	if got := outputOf(t, r.out, st); strings.Contains(got, "13812345678") {
		t.Fatalf("the retry published what was built before the choice: %q", got)
	}
}

// The authorization of a single-file task covers what the person saw when exporting. A file sent back to review
// and decided again waits for another export; keeping the old one would publish a choice nobody exported.
func TestADecisionTakesBackTheExportOfAQuickTask(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("quick", needsReview, "awaiting_review")
	job := st.Files[0].ID
	if err := r.e.Publish(r.ctx, st.ID, false, ""); err != nil { // authorized, and the file is under review: what validation sending it back leaves
		t.Fatal(err)
	}
	if st, _ = r.e.Status(r.ctx, st.ID, 10); !st.Authorized {
		t.Fatalf("the precondition: the export did not authorize: %+v", st)
	}
	fs, err := r.e.Findings(r.ctx, job, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatal(fs, err)
	}
	ds := make([]FindingDecision, len(fs))
	for i, f := range fs {
		ds[i] = FindingDecision{ID: f.ID}
	}
	if err = r.e.DecideFindings(r.ctx, job, ds); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" || st.Authorized {
		t.Fatalf("published on the old export: %+v %v", st, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(r.out, "*.txt")); len(leftovers) != 0 {
		t.Fatalf("delivered without another export: %v", leftovers)
	}
	// it is still waiting for a person, so it gives way
	if _, err = r.imp("quick", true, r.file(noReview)); err != nil {
		t.Fatalf("the next task: %v", err)
	}
	if err = r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if st = waitCompleted(t, r.e, st.ID); strings.Contains(outputOf(t, r.out, st), "110101199003074519") {
		t.Fatalf("the choice was not what the export published: %+v", st)
	}
}

// A selection made by hand, or taken back, changes what is published as a decision does.
func TestAManualSelectionTakesBackTheExportOfAQuickTask(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("quick", needsReview, "awaiting_review")
	job := st.Files[0].ID
	exported := func(step string) {
		t.Helper()
		if err := r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
			t.Fatal(err)
		}
		now, err := r.e.Status(r.ctx, st.ID, 10)
		if err != nil || !now.Authorized {
			t.Fatalf("before %s the export did not authorize: %+v %v", step, now, err)
		}
	}
	taken := func(step string) {
		t.Helper()
		if now, err := r.e.Status(r.ctx, st.ID, 10); err != nil || now.Authorized {
			t.Fatalf("%s left the export standing: %+v %v", step, now, err)
		}
	}
	units, err := r.e.Document(r.ctx, job, 0, 100)
	if err != nil || len(units) == 0 {
		t.Fatal(units, err)
	}
	exported("the selection")
	manual, err := r.e.AddManual(r.ctx, job, units[0].Seq, 0, 2, "person")
	if err != nil {
		t.Fatal(err)
	}
	taken("the selection")
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	exported("the withdrawal")
	if err = r.e.WithdrawManual(r.ctx, job, manual); err != nil {
		t.Fatal(err)
	}
	taken("the withdrawal")
}

// A batch states its authorization up front for files yet to come: a decision does not take it back (control).
func TestADecisionDoesNotTakeBackTheAuthorizationOfABatch(t *testing.T) {
	r := newParkRig(t)
	st, err := r.imp("batch", true, r.file(needsReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "awaiting_review" {
		t.Fatalf("%+v %v", st, err)
	}
	fs, err := r.e.Findings(r.ctx, st.Files[0].ID, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatal(fs, err)
	}
	ds := make([]FindingDecision, len(fs))
	for i, f := range fs {
		ds[i] = FindingDecision{ID: f.ID}
	}
	if err = r.e.DecideFindings(r.ctx, st.Files[0].ID, ds); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, r.e, st.ID)
}

// A write of choices can land just after a pause. The resume must not go on from what was built before them.
func TestAChoiceMadeUnderAPauseIsWhatItsResumePublishes(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("quick", noReview, "ready_to_publish")
	job := st.Files[0].ID
	decide := func(keep bool) {
		t.Helper()
		units, err := r.e.Document(r.ctx, job, 0, 100)
		if err != nil || len(units) == 0 || len(units[0].Findings) == 0 {
			t.Fatalf("the document of the task: %+v %v", units, err)
		}
		if err = r.e.DecideFindings(r.ctx, job, []FindingDecision{{ID: units[0].Findings[0].ID, Keep: keep}}); err != nil {
			t.Fatal(err)
		}
	}
	decide(true)
	var err error
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("after keeping: %+v %v", st, err)
	}
	if err = r.e.Pause(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	decide(false)
	if err = r.e.Resume(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	st = waitCompleted(t, r.e, st.ID)
	if got := outputOf(t, r.out, st); strings.Contains(got, "13812345678") {
		t.Fatalf("the resume published what was built before the choice: %q", got)
	}
}

// The screens leave a task's choices with the kernel when it goes to “最近”: they have to be there after a restart.
func TestTheChoicesOnAParkedTaskSurviveARestart(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("quick", noReview, "ready_to_publish")
	job := st.Files[0].ID
	units, err := r.e.Document(r.ctx, job, 0, 100)
	if err != nil || len(units) == 0 || len(units[0].Findings) == 0 {
		t.Fatalf("the document: %+v %v", units, err)
	}
	id := units[0].Findings[0].ID
	if units[0].Findings[0].Decision == "keep" {
		t.Fatal("kept before anybody chose")
	}
	if _, err = r.imp("quick", true, r.file(noReview)); err != nil {
		t.Fatal(err)
	}
	if err = r.e.DecideFindings(r.ctx, job, []FindingDecision{{ID: id, Keep: true}}); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	r.e.Close()
	r.e = openEngine(t, r.ws, r.key, true)
	if units, err = r.e.Document(r.ctx, job, 0, 100); err != nil || len(units) == 0 || len(units[0].Findings) == 0 {
		t.Fatalf("the document after the restart: %+v %v", units, err)
	}
	if got := units[0].Findings[0].Decision; got != "keep" {
		t.Fatalf("the choice after the restart: %q", got)
	}
}

func outputOf(t *testing.T, dir string, st BatchStatus) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, st.Files[0].Output))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A11: a parked task goes on to delivery, and neither result depends on the other having been there.
func TestAParkedTaskIsDeliveredAndReleasedAlongsideTheNewOne(t *testing.T) {
	alone := newParkRig(t)
	st := alone.waiting("quick", noReview, "ready_to_publish")
	if err := alone.e.Publish(alone.ctx, st.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	st = waitCompleted(t, alone.e, st.ID)
	want := outputOf(t, alone.out, st)
	if strings.Contains(want, "13812345678") {
		t.Fatalf("the phone number was delivered: %q", want)
	}

	r := newParkRig(t)
	old := r.waiting("quick", noReview, "ready_to_publish")
	next, err := r.imp("quick", true, r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: next.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, old.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{old.ID, next.ID} {
		done := waitCompleted(t, r.e, id)
		if got := outputOf(t, r.out, done); got != want {
			t.Fatalf("%s delivered %q, alone it is %q", id, got, want)
		}
	}
	if list, err := r.e.Unfinished(r.ctx); err != nil || len(list) != 0 {
		t.Fatalf("still listed %+v %v", list, err)
	}
	// both slots are free again
	if _, err = r.imp("quick", false, r.file(noReview)); err != nil {
		t.Fatalf("the slot after both ended: %v", err)
	}
}

// A decision on a parked file redoes it as on any other, without touching the task on screen.
func TestAParkedFileCanStillBeDecided(t *testing.T) {
	r := newParkRig(t)
	old := r.waiting("quick", needsReview, "awaiting_review")
	if _, err := r.imp("quick", true, r.file(noReview)); err != nil {
		t.Fatal(err)
	}
	fs, err := r.e.Findings(r.ctx, old.Files[0].ID, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatal(fs, err)
	}
	ds := make([]FindingDecision, len(fs))
	for i, f := range fs {
		ds[i] = FindingDecision{ID: f.ID}
	}
	if err = r.e.DecideFindings(r.ctx, old.Files[0].ID, ds); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, old.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	done := waitCompleted(t, r.e, old.ID)
	if got := outputOf(t, r.out, done); strings.Contains(got, "110101199003074519") {
		t.Fatalf("the decided value was delivered: %q", got)
	}
}

// A12: however many were parked, a restart lists them all, paused; nothing runs before it is continued.
func TestEveryParkedTaskIsListedAfterARestart(t *testing.T) {
	r := newParkRig(t)
	var ids []string
	for range 4 {
		ids = append(ids, r.waiting("quick", needsReview, "awaiting_review").ID)
	}
	ids = append(ids, r.waiting("batch", needsReview, "awaiting_review").ID)
	if err := r.e.Close(); err != nil {
		t.Fatal(err)
	}
	var stages atomic.Int32
	r.e = openEngineWith(t, r.ws, r.key, false, nil)
	r.e.beforeDispatch = func(kernel.StageKind) { stages.Add(1) }
	list, err := r.e.Unfinished(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(ids) {
		t.Fatalf("listed %d of %d: %+v", len(list), len(ids), list)
	}
	parked := map[string]int{}
	for i, b := range list {
		if b.ID != ids[i] || b.Files[0].State != "paused" || b.Active != 0 {
			t.Fatalf("%d: %+v", i, b)
		}
		if b.Parked {
			parked[b.Kind]++
		}
	}
	if parked["quick"] != 3 { // the fourth holds the slot of its kind
		t.Fatalf("parked %v", parked)
	}
	time.Sleep(100 * time.Millisecond)
	if n := stages.Load(); n != 0 {
		t.Fatalf("%d stages dispatched before anything was continued", n)
	}
	// continued, it is back where it was
	if err = r.e.Resume(r.ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	st, err := r.e.Wait(r.ctx, ids[0])
	if err != nil || st.Files[0].State != "awaiting_review" || !st.Parked {
		t.Fatalf("%+v %v", st, err)
	}
	// and a task that was waiting before the restart makes room again, paused
	if _, err = r.imp("quick", true, r.file(noReview)); err != nil {
		t.Fatalf("the holder paused by the restart (缺陷 3): %v", err)
	}
}

// A13: retention applies to a parked task like any other, and Clean still refuses it until it ended.
func TestAParkedTaskIsKeptForItsRetentionAndNotLonger(t *testing.T) {
	r := newParkRig(t)
	if _, err := r.e.SettingsSave(r.ctx, settingsWith(7, "comfort")); err != nil {
		t.Fatal(err)
	}
	first := r.waiting("quick", needsReview, "awaiting_review")
	second := r.waiting("quick", needsReview, "awaiting_review")
	if err := r.e.Clean(r.ctx, first.ID); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Clean of a parked task: %v", err)
	}
	r.e.Close()
	at := func(days float64) func(*Options) {
		return func(o *Options) {
			o.Now = func() time.Time { return time.Now().Add(time.Duration(days * 24 * float64(time.Hour))) }
		}
	}
	r.e = openEngineWith(t, r.ws, r.key, false, at(6.9))
	if bs, _ := r.e.Unfinished(r.ctx); len(bs) != 2 {
		t.Fatalf("within 7 days: %+v (%s %s)", bs, first.ID, second.ID)
	}
	r.e.Close()
	r.e = openEngineWith(t, r.ws, r.key, false, at(7.1))
	if bs, _ := r.e.Unfinished(r.ctx); len(bs) != 0 {
		t.Fatalf("past 7 days: %+v", bs)
	}
	if n := dirEntries(filepath.Join(r.ws, "resources")); n != 0 {
		t.Fatalf("%d resource files left behind", n)
	}
}

// A15, what holds without a model: a parked task keeps no attempt and takes no place in the scheduler while the
// next one runs. modelSem (capacity 1) is the same one any two tasks share.
func TestAParkedTaskHoldsNothingWhileTheNextOneRuns(t *testing.T) {
	r := newParkRig(t)
	if _, err := r.e.SettingsSave(r.ctx, settingsWith(7, "comfort")); err != nil {
		t.Fatal(err)
	}
	old := r.waiting("quick", needsReview, "awaiting_review")
	if s := r.e.queue.State(); s.Running != 0 || s.Pending != 0 {
		t.Fatalf("while only a parked-to-be task waits: %+v", s)
	}
	next, err := r.imp("quick", true, r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: next.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	max := budgetFor("comfort").MaxRunning
	done := make(chan struct{})
	go func() { defer close(done); waitCompleted(t, r.e, next.ID) }()
	for {
		if s := r.e.queue.State(); s.Running > max {
			t.Fatalf("%d stages run, the tier allows %d", s.Running, max)
		}
		select {
		case <-done:
			ms, err := r.e.members(r.ctx, old.ID)
			if err != nil || ms[0].AttemptID != "" {
				t.Fatalf("the parked file holds an attempt: %+v %v", ms, err)
			}
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

const phoneNumber = "13812345678"

// decisionRig holds one file whose only finding the person has chosen to keep, ready to publish and not yet told to:
// a document made from that choice has the number in it, and one made from a later “redact” has not.
type decisionRig struct {
	*parkRig
	batch, job string
	finding    int64
}

func newDecisionRig(t *testing.T, kind string) *decisionRig {
	t.Helper()
	r := &decisionRig{parkRig: newParkRig(t)}
	st := r.waiting(kind, noReview, "ready_to_publish")
	r.batch, r.job = st.ID, st.Files[0].ID
	fs, err := r.e.Findings(r.ctx, r.job, 0, 50)
	if err != nil || len(fs) != 1 {
		t.Fatalf("findings %+v %v", fs, err)
	}
	r.finding = fs[0].ID
	if err = r.e.DecideFindings(r.ctx, r.job, []FindingDecision{{ID: r.finding, Keep: true}}); err != nil {
		t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, r.batch); err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("after keeping: %+v %v", st, err)
	}
	r.keptInKernel()
	return r
}

func (r *decisionRig) redact() error {
	return r.e.DecideFindings(r.ctx, r.job, []FindingDecision{{ID: r.finding, Keep: false}})
}

func (r *decisionRig) decisionInKernel() string {
	r.t.Helper()
	fs, err := r.e.Findings(r.ctx, r.job, 0, 50)
	if err != nil || len(fs) != 1 {
		r.t.Fatalf("findings %+v %v", fs, err)
	}
	return fs[0].Decision
}

func (r *decisionRig) keptInKernel() {
	r.t.Helper()
	if d := r.decisionInKernel(); d != "keep" {
		r.t.Fatalf("the kernel holds %q, want the choice it refused to change: keep", d)
	}
}

// refused asserts the answer every entry for changing a decision gives, and that none of them changed anything.
func (r *decisionRig) refused() {
	r.t.Helper()
	before := r.state(r.batch, 0)
	for name, try := range map[string]func() error{
		"DecideFindings": r.redact,
		"DecideFinding":  func() error { return r.e.DecideFinding(r.ctx, r.job, r.finding, false) },
		"AddManual":      func() error { _, err := r.e.AddManual(r.ctx, r.job, 1, 0, 2, "person"); return err },
		"WithdrawManual": func() error { return r.e.WithdrawManual(r.ctx, r.job, r.finding) },
	} {
		if err := try(); !errors.Is(err, ErrDecisionsClosed) {
			r.t.Errorf("%s on a %s file: %v, want %v", name, before, err, ErrDecisionsClosed)
		}
	}
	r.keptInKernel()
	if s := r.state(r.batch, 0); s != before {
		r.t.Fatalf("a refused change moved the file from %s to %s", before, s)
	}
}

// document waits for the batch to end and reads what it delivered.
func (r *decisionRig) document() string {
	r.t.Helper()
	st, err := r.e.Wait(r.ctx, r.batch)
	if err != nil {
		r.t.Fatal(err)
	}
	f := st.Files[0]
	if f.State != "completed" || f.Output == "" {
		r.t.Fatalf("the file ended as %+v", f)
	}
	raw, err := os.ReadFile(filepath.Join(r.out, f.Output))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

func (r *decisionRig) stageStopped() {
	r.t.Helper()
	eventually(r.t, "stopped", func() bool { return r.e.queue.State().Running == 0 && !r.e.disp.Running(r.job) })
}

// The issue itself: the document is out, and the person changes a choice on the file.
func TestAChangeOfDecisionsOnADeliveredFileIsRefusedAndChangesNothing(t *testing.T) {
	r := newDecisionRig(t, "batch")
	if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	if doc := r.document(); !strings.Contains(doc, phoneNumber) {
		t.Fatalf("the precondition: the document has the kept number: %q", doc)
	}
	r.refused()
}

// While the publish runs nothing can follow a change any more: the answer says so, and what comes out is the document
// of what the kernel still holds, which is what the person was told.
func TestAChangeOfDecisionsWhileTheFileIsPublishingIsRefused(t *testing.T) {
	for _, kind := range []string{"batch", "quick"} {
		t.Run(kind, func(t *testing.T) {
			r := newDecisionRig(t, kind)
			held, release := holdStage(t, r.e, kernel.StagePublish, nil)
			if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
				t.Fatal(err)
			}
			eventually(t, "at the publish", func() bool { return held.Load() == 1 })
			if s := r.state(r.batch, 0); s != "publishing" {
				t.Fatalf("the file is %s", s)
			}
			r.refused()
			// nothing about the file changed, the authorization of a quick task neither: its publish goes through
			var granted bool
			if err := r.e.ws.Commit(r.ctx, func(tx store.Tx) (err error) {
				run, err := getRun(tx, r.batch)
				granted = run.Grant
				return err
			}); err != nil || !granted {
				t.Fatalf("a refused change took the authorization back: %v %v", granted, err)
			}
			release()
			if doc := r.document(); !strings.Contains(doc, phoneNumber) {
				t.Fatalf("the document is not the one of what the kernel holds: %q", doc)
			}
		})
	}
}

// “Too late” holds as long as the publish does. The screen counts on that: when the file comes back, what was refused is
// a failed write it sends again, and the next document has to be the one of that choice, not the one that did not get out.
func TestAChoiceRefusedAsTooLateCanBeMadeOnceThePublishGaveUp(t *testing.T) {
	r := newDecisionRig(t, "batch")
	held, release := holdStage(t, r.e, kernel.StagePublish, nil)
	if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the publish", func() bool { return held.Load() == 1 })
	r.refused()

	st, err := r.e.Status(r.ctx, r.batch, 10)
	if err != nil {
		t.Fatal(err)
	}
	taken := filepath.Join(r.out, r.batch+"-"+st.Files[0].Name+".txt")
	if err = os.WriteFile(taken, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	eventually(t, "given up", func() bool { return r.state(r.batch, 0) == "blocked" })

	if err = r.redact(); err != nil {
		t.Fatalf("the file came back, and the choice is still refused: %v", err)
	}
	if err = os.Remove(taken); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Retry(r.ctx, r.batch, nil); err != nil {
		t.Fatal(err)
	}
	if err = r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	if doc := r.document(); strings.Contains(doc, phoneNumber) {
		t.Fatalf("the document went out without the choice: %q", doc)
	}
}

// A partial result is as delivered as a complete one. Nothing in the test files makes one, so the file is taken there
// through the same call the stage ends with.
func TestAChangeOfDecisionsOnAPartialResultIsRefused(t *testing.T) {
	r := newDecisionRig(t, "batch")
	held, release := holdStage(t, r.e, kernel.StagePublish, nil)
	if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the publish", func() bool { return held.Load() == 1 })
	if err := r.e.ws.Commit(r.ctx, func(tx store.Tx) error {
		j, err := r.e.jobs.Get(tx, r.job)
		if err != nil {
			return err
		}
		_, err = r.e.jobs.CompleteStage(tx, kernel.AttemptRef{JobID: j.ID, AttemptID: j.AttemptID, OwnerGeneration: r.e.ws.Generation(), JobVersion: j.Version},
			sched.StageOutcome{Kind: sched.Completed, Detail: jobs.DetailPartial})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s := r.state(r.batch, 0); s != "partial" {
		t.Fatalf("the file is %s", s)
	}
	r.refused()
	release()
	r.stageStopped()
}

// #40's finding. A publish stopped by a pause before anything went out has nothing in the way of a new choice: the
// resume has to deliver that one, not what was made before it.
func TestADecisionUnderAPausedPublishIsWhatItsResumeDelivers(t *testing.T) {
	r := newDecisionRig(t, "batch")
	held, release := holdStage(t, r.e, kernel.StagePublish, nil)
	if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the publish", func() bool { return held.Load() == 1 })
	if err := r.e.Pause(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	release()
	r.stageStopped()
	if err := r.redact(); err != nil {
		t.Fatalf("a choice on a paused publish that sent nothing: %v", err)
	}
	if s := r.state(r.batch, 0); s != "paused" {
		t.Fatalf("the choice ended the pause: %s", s)
	}
	if d := r.decisionInKernel(); d != "redact" {
		t.Fatalf("the kernel holds %q", d)
	}
	if err := r.e.Resume(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	if doc := r.document(); strings.Contains(doc, phoneNumber) {
		t.Fatalf("the resume delivered what was made before the choice: %q", doc)
	}
}

// A pause does not tell whether the stopped publish got its document out: its record does. This one was on its way
// (the file in place, not yet on record) when the pause came, and a choice cannot call it back.
func TestADecisionUnderAPausedPublishThatGotOutIsRefused(t *testing.T) {
	r := newDecisionRig(t, "batch")
	placed, goOn := make(chan struct{}), make(chan struct{})
	r.e.pub.Placed = func() {
		close(placed)
		<-goOn
	}
	if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	<-placed
	if err := r.e.Pause(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	r.refused() // the intent is still open
	close(goOn)
	r.stageStopped()
	r.refused() // now it is a delivery on record
	if err := r.e.Resume(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	if doc := r.document(); !strings.Contains(doc, phoneNumber) {
		t.Fatalf("the document: %q", doc)
	}
	r.refused()
}

// What decides is on record, not in memory: a restart changes neither answer.
func TestAPausedPublishAnswersTheSameAfterARestart(t *testing.T) {
	restart := func(r *decisionRig) {
		t.Helper()
		if err := r.e.Close(); err != nil {
			t.Fatal(err)
		}
		r.e = openEngine(t, r.ws, r.key, false)
		if s := r.state(r.batch, 0); s != "paused" {
			t.Fatalf("after the restart the file is %s", s)
		}
	}
	t.Run("nothing went out", func(t *testing.T) {
		r := newDecisionRig(t, "batch")
		held, release := holdStage(t, r.e, kernel.StagePublish, nil)
		if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, "at the publish", func() bool { return held.Load() == 1 })
		if err := r.e.Pause(r.ctx, r.batch); err != nil {
			t.Fatal(err)
		}
		release()
		r.stageStopped()
		restart(r)
		if err := r.redact(); err != nil {
			t.Fatal(err)
		}
		if err := r.e.Resume(r.ctx, r.batch); err != nil {
			t.Fatal(err)
		}
		if doc := r.document(); strings.Contains(doc, phoneNumber) {
			t.Fatalf("the resume delivered what was made before the choice: %q", doc)
		}
	})
	t.Run("it went out", func(t *testing.T) {
		r := newDecisionRig(t, "batch")
		placed, goOn := make(chan struct{}), make(chan struct{})
		r.e.pub.Placed = func() {
			close(placed)
			<-goOn
		}
		if err := r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
			t.Fatal(err)
		}
		<-placed
		if err := r.e.Pause(r.ctx, r.batch); err != nil {
			t.Fatal(err)
		}
		close(goOn)
		r.stageStopped()
		restart(r)
		r.refused()
		if err := r.e.Resume(r.ctx, r.batch); err != nil {
			t.Fatal(err)
		}
		if doc := r.document(); !strings.Contains(doc, phoneNumber) {
			t.Fatalf("the document: %q", doc)
		}
	})
}

// A file on its way back to a stage past resolution would go on from the old decisions, like one at that stage. Taken
// there under a pause, so that no notification of the old stage is on its way to run in the new one's place (#40).
func TestADecisionOnAFileQueuedPastResolutionIsWhatItGoesOnWith(t *testing.T) {
	r := newDecisionRig(t, "batch")
	if err := r.e.Pause(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	position := func() (p jobs.Snapshot) {
		t.Helper()
		if err := r.e.ws.Commit(r.ctx, func(tx store.Tx) error {
			j, err := r.e.jobs.Get(tx, r.job)
			p = j.Position
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := r.e.ws.Commit(r.ctx, func(tx store.Tx) error {
		j, err := r.e.jobs.Get(tx, r.job)
		if err != nil {
			return err
		}
		_, err = r.e.jobs.Apply(tx, r.job, j.Version, jobs.Event{Kind: jobs.EvInvalidate, Stage: kernel.StageExport})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if p := position(); p.State != jobs.Paused || p.Resume != jobs.Queued || p.Cursor != kernel.StageExport {
		t.Fatalf("the precondition: to go back to the export after the pause: %+v", p)
	}
	if err := r.redact(); err != nil {
		t.Fatal(err)
	}
	if p := position(); p.State != jobs.Paused || p.Resume != jobs.Queued || p.Cursor != kernel.StageResolve {
		t.Fatalf("the decision left the file going on from %+v", p)
	}
	if err := r.e.Resume(r.ctx, r.batch); err != nil {
		t.Fatal(err)
	}
	st, err := r.e.Wait(r.ctx, r.batch)
	if err != nil || st.Files[0].State != "ready_to_publish" {
		t.Fatalf("after the resume: %+v %v", st, err)
	}
	if err = r.e.Publish(r.ctx, r.batch, false, ""); err != nil {
		t.Fatal(err)
	}
	if doc := r.document(); strings.Contains(doc, phoneNumber) {
		t.Fatalf("the file went on from the old decisions: %q", doc)
	}
}

// A taken change does what any change of a file's state does: the stage it makes pointless stops at once...
func TestAChangeOfDecisionsStopsTheStageItReplaces(t *testing.T) {
	r := newDecisionRig(t, "batch")
	started := make(chan context.Context, 1)
	r.e.stageContext = func(ctx context.Context, _ string, stage kernel.StageKind) {
		if stage == kernel.StageExport {
			started <- ctx
		}
	}
	held, release := holdStage(t, r.e, kernel.StageExport, nil)
	if err := r.redact(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "at the export", func() bool { return held.Load() == 1 })
	stage := <-started
	if stage.Err() != nil {
		t.Fatalf("the precondition: the stage runs: %v", stage.Err())
	}
	r.e.stageContext = nil
	if err := r.e.DecideFindings(r.ctx, r.job, []FindingDecision{{ID: r.finding, Keep: true}}); err != nil {
		t.Fatal(err)
	}
	if stage.Err() == nil {
		t.Fatal("the stage made from the decisions before this one keeps running")
	}
	release()
}

// ...and the screen hears of it.
func TestAChangeOfDecisionsIsToldToTheScreen(t *testing.T) {
	r := newDecisionRig(t, "batch")
	gate := make(chan struct{})
	r.e.beforeDispatch = func(kernel.StageKind) { <-gate } // nothing else moves the file, so what is heard comes from the change
	t.Cleanup(func() { close(gate) })
	var heard atomic.Int32
	defer r.e.Subscribe(func(string) { heard.Add(1) })()
	if err := r.redact(); err != nil {
		t.Fatal(err)
	}
	if heard.Load() == 0 {
		t.Fatal("the screen was not told about the change")
	}
}

// One call is one change: a list that cannot be written whole leaves the kernel as it was, not with half a choice that
// no document follows.
func TestAListOfDecisionsThatCannotBeWrittenLeavesNothingBehind(t *testing.T) {
	r := newDecisionRig(t, "batch")
	err := r.e.DecideFindings(r.ctx, r.job, []FindingDecision{{ID: r.finding, Keep: false}, {ID: r.finding + 9999, Keep: true}})
	if err == nil {
		t.Fatal("a finding that does not exist was accepted")
	}
	r.keptInKernel()
	if s := r.state(r.batch, 0); s != "ready_to_publish" {
		t.Fatalf("the file is %s", s)
	}
}

// Not refused too widely: nothing goes out of these files, and a person's choice on them harms nothing.
func TestADecisionOnAFileThatDeliversNothingIsNotRefused(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("batch", needsReview, "awaiting_review")
	job := st.Files[0].ID
	fs, err := r.e.Findings(r.ctx, job, 0, 50)
	if err != nil || len(fs) == 0 {
		t.Fatalf("findings %+v %v", fs, err)
	}
	if err = r.e.Skip(r.ctx, job); err != nil {
		t.Fatal(err)
	}
	if s := r.state(st.ID, 0); s != "skipped" {
		t.Fatalf("the file is %s", s)
	}
	if err = r.e.DecideFindings(r.ctx, job, []FindingDecision{{ID: fs[0].ID, Keep: true}}); err != nil {
		t.Fatalf("a choice on a skipped file: %v", err)
	}
	if s := r.state(st.ID, 0); s != "skipped" {
		t.Fatalf("the choice moved the file to %s", s)
	}
}

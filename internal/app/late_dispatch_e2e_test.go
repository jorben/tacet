//go:build darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

// lateNotice holds one stage notification back, on the real path, until the test lets it land. Once its dispatch has
// ended the notification has been dealt with, however it ended, so nothing here sleeps.
type lateNotice struct {
	t     *testing.T
	r     *parkRig
	stage kernel.StageKind
	nth   int32  // the nth notification of the stage after arming is the one held
	first func() // runs in the first notification of the stage after arming, when that is not the one held
	job   string // set before arming
	armed atomic.Bool
	seen  atomic.Int32
	held  chan chan struct{} // receives the dispatch of the held notification
	gate  chan struct{}
	once  sync.Once
}

func newLateNotice(t *testing.T, stage kernel.StageKind, nth int32) *lateNotice {
	t.Helper()
	l := &lateNotice{t: t, r: newParkRig(t), stage: stage, nth: nth, held: make(chan chan struct{}, 1), gate: make(chan struct{})}
	t.Cleanup(l.open) // before the engine closes (cleanups run last first)
	l.r.e.beforeDispatch = l.hold
	l.r.e.dispatchFailed = func() { t.Error("a notification that no longer applies was recorded as a failed stage") }
	return l
}

func (l *lateNotice) open() { l.once.Do(func() { close(l.gate) }) }

func (l *lateNotice) hold(stage kernel.StageKind) {
	if stage != l.stage || !l.armed.Load() {
		return
	}
	switch l.seen.Add(1) {
	case l.nth:
		// This is the newest dispatch of the file: the stage after it cannot come while an earlier one is held.
		done, ok := l.r.e.nextStage.Load(l.job)
		if !ok {
			l.t.Error("the held notification has no dispatch on record")
			return
		}
		l.held <- done.(chan struct{})
		<-l.gate
	case 1:
		if l.first != nil {
			l.first()
		}
	}
}

// land lets the held notification land and waits until it has been dealt with.
func (l *lateNotice) land(dispatch <-chan struct{}) {
	l.open()
	<-dispatch
}

// awaitingTwoDecisions brings a file with two findings to review, with no authorization to publish.
func (r *parkRig) awaitingTwoDecisions() (batch, job string, fs []Finding) {
	r.t.Helper()
	st, err := r.imp("quick", r.file(needsReview))
	if err != nil {
		r.t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID}); err != nil {
		r.t.Fatal(err)
	}
	if st, err = r.e.Wait(r.ctx, st.ID); err != nil {
		r.t.Fatal(err)
	}
	if st.Files[0].State != "awaiting_review" {
		r.t.Fatalf("%+v", st)
	}
	fs, err = r.e.Findings(r.ctx, st.Files[0].ID, 0, 50)
	if err != nil || len(fs) != 2 {
		r.t.Fatal(fs, err)
	}
	return st.ID, st.Files[0].ID, fs
}

// decideTwice makes the two decisions in a row, as a person does, so that two resolve notifications are out. The first
// runs on; the second stays behind, and what is returned is its dispatch.
func (l *lateNotice) decideTwice(job string, fs []Finding) <-chan struct{} {
	l.t.Helper()
	r := l.r
	arrived, wait := make(chan struct{}), make(chan struct{})
	goOn := sync.OnceFunc(func() { close(wait) })
	l.t.Cleanup(goOn)
	l.first = func() { close(arrived); <-wait }
	l.job = job
	l.armed.Store(true)
	if err := r.e.DecideFinding(r.ctx, job, fs[0].ID, true); err != nil {
		l.t.Fatal(err)
	}
	<-arrived // the first resolve is on its way and has not begun: the second decision comes before it
	if err := r.e.DecideFinding(r.ctx, job, fs[1].ID, false); err != nil {
		l.t.Fatal(err)
	}
	dispatch := <-l.held
	goOn()
	return dispatch
}

// publishes checks that the file goes on to its document once authorized, made from the last decisions.
func (r *parkRig) publishes(batch string) {
	r.t.Helper()
	if err := r.e.Publish(r.ctx, batch, false, ""); err != nil {
		r.t.Fatal(err)
	}
	st, err := r.e.Wait(r.ctx, batch)
	if err != nil {
		r.t.Fatal(err)
	}
	if st.Files[0].State != "completed" || st.Delivered != 1 {
		r.t.Fatalf("after the authorization: %+v", st)
	}
	raw, err := os.ReadFile(filepath.Join(r.out, st.Files[0].Output))
	if err != nil {
		r.t.Fatal(err)
	}
	if !strings.Contains(string(raw), "110101199003074519") || strings.Contains(string(raw), "110101199003074518") {
		r.t.Fatalf("the document is not made from the last decisions: %q", raw)
	}
	// the counterpart of stillReady: the same look at the folder does find one
	if docs := r.documents(); len(docs) != 1 {
		r.t.Fatalf("documents: %v", docs)
	}
}

// documents lists what the output folder holds apart from the reports and the temporary files they are written through:
// the report of a file is written after the file reads as delivered, so one may still be on its way.
func (r *parkRig) documents() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.out)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, f := range entries {
		if !strings.Contains(f.Name(), ".report.") && !strings.HasPrefix(f.Name(), ".tacet-") {
			out = append(out, f.Name())
		}
	}
	return out
}

// stillReady checks that the file is where it was: ready, not delivered, and with no document.
func (r *parkRig) stillReady(batch string) {
	r.t.Helper()
	st, err := r.e.Wait(r.ctx, batch)
	if err != nil {
		r.t.Fatal(err)
	}
	if f := st.Files[0]; f.State != "ready_to_publish" || st.Delivered != 0 {
		r.t.Fatalf("the late notification changed the file: state %q, detail %q, delivered %d, documents %v", f.State, f.Detail, st.Delivered, r.documents())
	}
	if docs := r.documents(); len(docs) != 0 {
		r.t.Fatalf("documents with no authorization: %v", docs)
	}
}

// A resolve notification left behind by a second decision lands between resolve and export: the file is exporting and
// has no attempt yet. Run as what the notification says, its export never happens and validation finds no candidate (#40).
func TestALateNotificationBetweenResolveAndExportDoesNotTakeTheFilesStage(t *testing.T) {
	late := newLateNotice(t, kernel.StageResolve, 2)
	r := late.r
	exporting, landed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.e.beforeDispatch = func(stage kernel.StageKind) {
		late.hold(stage)
		if stage == kernel.StageExport && late.armed.Load() {
			once.Do(func() { close(exporting); <-landed })
		}
	}
	batch, id, fs := r.awaitingTwoDecisions()
	dispatch := late.decideTwice(id, fs)
	<-exporting // the file is exporting, and its export has not begun
	late.land(dispatch)
	close(landed)
	r.stillReady(batch)
	r.publishes(batch)
}

// The same notification lands on a file that stopped at ready_to_publish with no authorization. Run, it counts as the
// publish stage: the file reads as delivered, with no document (#40).
func TestALateNotificationOnAFileReadyToPublishDoesNotDeliverIt(t *testing.T) {
	late := newLateNotice(t, kernel.StageResolve, 2)
	r := late.r
	batch, id, fs := r.awaitingTwoDecisions()
	dispatch := late.decideTwice(id, fs)
	r.stillReady(batch) // the premise: this is where it stopped, before the notification lands
	late.land(dispatch)
	r.stillReady(batch)
	r.publishes(batch)
}

// A publish notification still on its way when a decision takes the authorization back lands on a file that is ready
// again. It names the stage the file would run, and still may not start it: being ready is not being authorized.
func TestALatePublishNotificationDoesNotStandInForTheAuthorization(t *testing.T) {
	late := newLateNotice(t, kernel.StagePublish, 1)
	r := late.r
	batch, id, fs := r.awaitingTwoDecisions()
	for _, f := range fs {
		if err := r.e.DecideFinding(r.ctx, id, f.ID, false); err != nil {
			t.Fatal(err)
		}
		if _, err := r.e.Wait(r.ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	r.stillReady(batch)
	late.job = id
	late.armed.Store(true)
	if err := r.e.Publish(r.ctx, batch, false, ""); err != nil {
		t.Fatal(err)
	}
	dispatch := <-late.held
	// this choice takes the authorization back and has the file made again
	if err := r.e.DecideFinding(r.ctx, id, fs[0].ID, true); err != nil {
		t.Fatal(err)
	}
	r.stillReady(batch)
	if st, err := r.e.Status(r.ctx, batch, 10); err != nil || st.Authorized {
		t.Fatalf("the premise: the file is no longer authorized: %+v %v", st, err)
	}
	late.land(dispatch)
	r.stillReady(batch)
	late.armed.Store(false)
	r.publishes(batch)
}

// A publish that was paused and resumed is queued with only its own notification to come. A decision meanwhile took the
// batch's authorization back, and that rule is about files leaving ready_to_publish: this one must not wait for ever.
// Whether that publish should go on with the choice made meanwhile is another question, not settled here: only that the
// notification is not dropped is asserted.
func TestAResumedPublishIsNotDroppedForTheAuthorizationADecisionTookBack(t *testing.T) {
	late := newLateNotice(t, kernel.StagePublish, 1)
	r := late.r
	batch, id, fs := r.awaitingTwoDecisions()
	for _, f := range fs {
		if err := r.e.DecideFinding(r.ctx, id, f.ID, false); err != nil {
			t.Fatal(err)
		}
		if _, err := r.e.Wait(r.ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	started, gate := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	var once sync.Once
	r.e.stageStarted = func(_ string, s kernel.StageKind) {
		if s == kernel.StagePublish {
			once.Do(func() { close(started); <-gate })
		}
	}
	if err := r.e.Publish(r.ctx, batch, false, ""); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := r.e.Pause(r.ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := r.e.DecideFinding(r.ctx, id, fs[0].ID, true); err != nil {
		t.Fatal(err)
	}
	release()
	if st, err := r.e.Status(r.ctx, batch, 10); err != nil || st.Authorized || st.Files[0].State != "paused" {
		t.Fatalf("the premise: paused, and no longer authorized: %+v %v", st, err)
	}
	late.job = id
	late.armed.Store(true)
	if err := r.e.Resume(r.ctx, batch); err != nil {
		t.Fatal(err)
	}
	dispatch := <-late.held
	if s := r.state(batch, 0); s != "queued" {
		t.Fatalf("the premise: queued with its notification on the way, not %s", s)
	}
	late.land(dispatch)
	if s := r.state(batch, 0); s == "queued" {
		t.Fatal("the notification was dropped, and no other is coming: the file stays queued")
	}
}

// Whatever reports a publish stage done, a file with no delivery on record does not read as delivered: this is the
// guard behind the ones above, for a way there nobody has found yet. A file that does publish goes through the same
// check: see publishes above.
func TestAPublishStageWithNoDeliveryOnRecordFailsTheFile(t *testing.T) {
	for name, detail := range map[string]string{"completed": "", "partial": jobsDetailPartial} {
		t.Run(name, func(t *testing.T) {
			r := newParkRig(t)
			batch, id, fs := r.awaitingTwoDecisions()
			for _, f := range fs {
				if err := r.e.DecideFinding(r.ctx, id, f.ID, false); err != nil {
					t.Fatal(err)
				}
				if _, err := r.e.Wait(r.ctx, batch); err != nil {
					t.Fatal(err)
				}
			}
			r.stillReady(batch)
			var a kernel.AttemptRef
			if err := r.e.ws.Commit(context.Background(), func(tx store.Tx) error {
				j, err := r.e.jobs.Get(tx, id)
				if err != nil {
					return err
				}
				a, err = r.e.jobs.BeginStage(tx, id, kernel.StagePublish, j.Version, r.e.ws.Generation())
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := r.e.complete(sched.JobView{Attempt: a, Stage: kernel.StagePublish}, done(detail)); err != nil {
				t.Fatal(err)
			}
			st, err := r.e.Status(r.ctx, batch, 10)
			if err != nil {
				t.Fatal(err)
			}
			if f := st.Files[0]; f.State != "failed" || f.Detail != "DELIVERY_MISSING" || st.Delivered != 0 {
				t.Fatalf("state %q, detail %q, delivered %d", f.State, f.Detail, st.Delivered)
			}
		})
	}
}

//go:build darwin

package app

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/snapshot"
)

// stageLog records the stages in the order the scheduler started them.
type stageLog struct {
	mu   sync.Mutex
	jobs []string
}

func (l *stageLog) add(job string) { l.mu.Lock(); l.jobs = append(l.jobs, job); l.mu.Unlock() }
func (l *stageLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.jobs...)
}

// queued waits until n stages wait in the paused queue: every dispatch has gone through.
func queued(t *testing.T, r *parkRig, n int) {
	t.Helper()
	eventually(t, "queued", func() bool { return r.e.queue.State().Pending == n })
}

// Two tasks of one kind are worked on at the same moment: the stage of each one waits until the other is running too,
// which it can only be if neither waits for the other to finish.
func TestTwoTasksOfOneKindAreWorkedOnAtTheSameTime(t *testing.T) {
	for _, kind := range []string{"quick", "batch"} {
		t.Run(kind, func(t *testing.T) {
			alone := newParkRig(t)
			st := alone.waiting(kind, noReview, "ready_to_publish")
			if err := alone.e.Publish(alone.ctx, st.ID, false, ""); err != nil {
				t.Fatal(err)
			}
			want := outputOf(t, alone.out, waitCompleted(t, alone.e, st.ID))

			r := &parkRig{t: t, ws: t.TempDir() + "/ws", in: t.TempDir(), out: t.TempDir(), key: &memKey{}, ctx: t.Context()}
			r.e = openEngineWith(t, r.ws, r.key, true, func(o *Options) { o.Budget = budgetFor("std") })
			t.Cleanup(func() { r.e.Close() })
			var running atomic.Int32
			release := make(chan struct{})
			var once sync.Once
			open := func() { once.Do(func() { close(release) }) }
			t.Cleanup(open) // before the engine closes (cleanups run last first)
			r.e.stageStarted = func(_ string, stage kernel.StageKind) {
				if stage == kernel.StageExtractDetect {
					running.Add(1)
					<-release
				}
			}
			begin := func() string {
				t.Helper()
				st, err := r.imp(kind, r.file(noReview))
				if err != nil {
					t.Fatalf("an import beside a task under way: %v", err)
				}
				if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
					t.Fatal(err)
				}
				return st.ID
			}
			first := begin()
			eventually(t, "running", func() bool { return running.Load() == 1 })
			second := begin()
			eventually(t, "running side by side", func() bool { return running.Load() == 2 })
			if s := r.e.queue.State(); s.Running != 2 || s.Running > budgetFor("std").MaxRunning {
				t.Fatalf("the scheduler: %+v", s)
			}
			open()
			for _, id := range []string{first, second} {
				if got := outputOf(t, r.out, waitCompleted(t, r.e, id)); got != want {
					t.Fatalf("%s delivered %q, alone it is %q", id, got, want)
				}
			}
			if list, err := r.e.Unfinished(r.ctx); err != nil || len(list) != 0 {
				t.Fatalf("still listed %+v %v", list, err)
			}
		})
	}
}

// A single file dropped behind a batch whose files are all queued is the next thing the scheduler starts.
func TestASingleFileGoesAheadOfABatchQueuedEarlier(t *testing.T) {
	r := newParkRig(t)
	var log stageLog
	r.e.stageStarted = func(job string, _ kernel.StageKind) { log.add(job) }
	files := make([]string, 12)
	for i := range files {
		files[i] = r.file(noReview)
	}
	batch, err := r.imp("batch", files...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.e.Preflight(r.ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	r.e.queue.PauseDispatch(true)
	if err = r.e.Start(r.ctx, StartRequest{Batch: batch.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	queued(t, r, len(files))
	before := len(log.list())
	quick, err := r.imp("quick", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	queued(t, r, len(files)+1)
	r.e.queue.PauseDispatch(false)
	if err = r.e.Start(r.ctx, StartRequest{Batch: quick.ID, Authorize: true}); err != nil { // waits for the inspection
		t.Fatal(err)
	}
	done := waitCompleted(t, r.e, quick.ID)
	// The inspection is the first thing started. Starting the task is the person's call and can let a stage of the
	// batch by; from there every stage of the file follows the last, with nothing of the batch between them.
	started := log.list()[before:]
	file := done.Files[0].ID
	var at []int
	for i, job := range started {
		if job == file {
			at = append(at, i)
		}
	}
	if len(at) < 5 || at[0] != 0 {
		t.Fatalf("the single file %s started at %v of %v", file, at, started)
	}
	for i := 2; i < len(at); i++ {
		if at[i] != at[i-1]+1 {
			t.Fatalf("a stage of the batch came between those of the single file %s: at %v of %v", file, at, started)
		}
	}
	if st := waitCompleted(t, r.e, batch.ID); st.Delivered != len(files) {
		t.Fatalf("the batch beside it: %+v", st)
	}
}

// A small batch imported after a large one does not wait behind all of its files.
func TestASmallBatchIsNotKeptBehindALargeOne(t *testing.T) {
	r := newParkRig(t)
	var log stageLog
	r.e.stageStarted = func(job string, _ kernel.StageKind) { log.add(job) }
	r.e.queue.PauseDispatch(true)
	files := make([]string, 12)
	for i := range files {
		files[i] = r.file(noReview)
	}
	if _, err := r.imp("batch", files...); err != nil {
		t.Fatal(err)
	}
	small, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	queued(t, r, len(files)+1)
	r.e.queue.PauseDispatch(false)
	if _, err = r.e.Preflight(r.ctx, small.ID); err != nil {
		t.Fatal(err)
	}
	started := log.list()
	id := small.Files[0].ID
	if len(started) < 2 || started[1] != id {
		t.Fatalf("the small batch's file %s started as %v", id, started)
	}
}

// Stopping and cleaning one task leaves the other of its kind alone.
func TestCancellingOneTaskLeavesTheOtherOfItsKindAlone(t *testing.T) {
	r := newParkRig(t)
	first := r.waiting("quick", needsReview, "awaiting_review")
	second := r.waiting("quick", noReview, "ready_to_publish")
	if err := r.e.Cancel(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.e.Clean(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.e.Publish(r.ctx, second.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := outputOf(t, r.out, waitCompleted(t, r.e, second.ID)); strings.Contains(got, "13812345678") {
		t.Fatalf("delivered %q", got)
	}
	if list, err := r.e.Unfinished(r.ctx); err != nil || len(list) != 0 {
		t.Fatalf("still listed %+v %v", list, err)
	}
}

// “继续” on a task that went on working while it was listed changes nothing about it: the screens call Resume on
// whatever they bring back, and no file runs or publishes twice.
func TestContinuingATaskUnderWayChangesNothing(t *testing.T) {
	r := newParkRig(t)
	var log stageLog
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	r.e.stageStarted = func(job string, stage kernel.StageKind) {
		log.add(job + ":" + string(stage))
		if stage == kernel.StageExport {
			<-release
		}
	}
	st, err := r.imp("batch", r.file(noReview))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.e.Start(r.ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "exporting", func() bool {
		return r.e.queue.State().Running == 1 && strings.HasSuffix(strings.Join(log.list(), " "), string(kernel.StageExport))
	})
	if err = r.e.Resume(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	open()
	done := waitCompleted(t, r.e, st.ID)
	if done.Delivered != 1 {
		t.Fatalf("%+v", done)
	}
	seen := map[string]int{}
	for _, s := range log.list() {
		seen[s]++
		if seen[s] > 1 {
			t.Fatalf("%s ran again: %v", s, log.list())
		}
	}
}

// A decision on a file of a batch keeps it ahead of the batch's other work as it goes through to delivery (AR-16).
func TestAFileDecidedOnStaysAheadOnItsWayToDelivery(t *testing.T) {
	r := newParkRig(t)
	st := r.waiting("batch", needsReview, "awaiting_review")
	job := st.Files[0].ID
	ms, err := r.e.members(r.ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stagePriority("batch", ms[0].Position); got != kernel.Background {
		t.Fatalf("before the decision: %v", got)
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
	if ms, err = r.e.members(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if got := stagePriority("batch", ms[0].Position); got != kernel.Interactive {
		t.Fatalf("after the decision: %v %+v", got, ms[0].Position)
	}
	// and across a restart: what was decided is stored with the file
	if _, err = r.e.Wait(r.ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	r.e.Close()
	r.e = openEngine(t, r.ws, r.key, false)
	if ms, err = r.e.members(r.ctx, st.ID); err != nil || !ms[0].Position.Reviewed {
		t.Fatalf("after a restart: %+v %v", ms, err)
	}
}

// A15: however many tasks want the model, one worker runs at a time. Each holder checks it is alone, under -race.
func TestOneModelWorkerRunsHoweverManyTasksWantIt(t *testing.T) {
	e, args, _ := probeEngine(t, workerNoBackend, false)
	m := e.modelByID(probeModel)
	const tasks = 4
	var holding, most atomic.Int32
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := e.modelRunner(t.Context(), kernel.AttemptRef{JobID: "t", AttemptID: "t_a" + string(rune('0'+i)), OwnerGeneration: e.ws.Generation(), JobVersion: 1},
				probeModel, &snapshot.ModelExecution{ModelID: probeModel, Digest: m.digest, Backend: "auto"})
			if err != nil {
				t.Error(err)
				return
			}
			n := holding.Add(1)
			for {
				old := most.Load()
				if n <= old || most.CompareAndSwap(old, n) {
					break
				}
			}
			holding.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Fatalf("%d model workers held at once", most.Load())
	}
	if s := starts(t, args); len(s) != tasks { // the control: they all did start one
		t.Fatalf("workers started: %q", s)
	}
}

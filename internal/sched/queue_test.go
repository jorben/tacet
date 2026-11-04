package sched

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/netgate"
)

type fakeRunner struct {
	stage kernel.StageKind
	claim kernel.ResourceClaim
	run   func(StageContext) StageOutcome
}

func (r *fakeRunner) Kind() kernel.StageKind             { return r.stage }
func (r *fakeRunner) Claim(JobView) kernel.ResourceClaim { return r.claim }
func (r *fakeRunner) Run(ctx StageContext) StageOutcome {
	if r.run != nil {
		return r.run(ctx)
	}
	return StageOutcome{Kind: Completed}
}

func budget() Budget {
	return Budget{MemoryBytes: 1000, ScratchBytes: 1000, CandidateBytes: 1000, QueueBytes: 1000, CPUSlots: 4, MaxRunning: 4, MaxQueued: 10, Workers: map[string]int{"format": 2}, HighWaterBytes: 800}
}
func claim() kernel.ResourceClaim {
	return kernel.ResourceClaim{MemoryBytes: 100, ScratchBytes: 100, CandidateBytes: 100, QueueBytes: 100, CPUSlots: 1, WorkerKind: "format"}
}
func job(id string, stage kernel.StageKind, priority kernel.Priority) JobView {
	return JobView{Attempt: kernel.AttemptRef{JobID: id, AttemptID: "a_" + id, OwnerGeneration: 1, JobVersion: 1}, Stage: stage, Priority: priority}
}
func queue(t *testing.T, b Budget, runners ...StageRunner) (*Queue, *netgate.Gate) {
	t.Helper()
	gate := netgate.NewGate()
	q, err := NewQueue(b, gate, runners...)
	if err != nil {
		t.Fatal(err)
	}
	return q, gate
}
func submit(t *testing.T, q *Queue, j JobView) {
	t.Helper()
	if err := q.Submit(j); err != nil {
		t.Fatal(err)
	}
}
func take(t *testing.T, q *Queue) *Ticket {
	t.Helper()
	ticket, reason := q.Take()
	if ticket == nil {
		t.Fatalf("admission blocked: %s", reason)
	}
	return ticket
}

func TestQueueInteractivePriorityAndDownstreamOrdering(t *testing.T) {
	runners := []StageRunner{}
	for _, stage := range []kernel.StageKind{kernel.StageExtractDetect, kernel.StageExport, kernel.StageValidate, kernel.StagePublish} {
		runners = append(runners, &fakeRunner{stage: stage, claim: claim()})
	}
	q, _ := queue(t, budget(), runners...)
	for _, j := range []JobView{job("extract", kernel.StageExtractDetect, kernel.Background), job("export", kernel.StageExport, kernel.Background), job("publish", kernel.StagePublish, kernel.Background), job("interactive", kernel.StageValidate, kernel.Interactive)} {
		submit(t, q, j)
	}
	for _, id := range []string{"interactive", "publish", "export", "extract"} {
		ticket := take(t, q)
		if ticket.Job().Attempt.JobID != id {
			t.Fatalf("got %s want %s", ticket.Job().Attempt.JobID, id)
		}
		ticket.Finish()
	}
}

func TestQueueEnforcesEachReservationDimension(t *testing.T) {
	for name, alter := range map[string]func(*Budget){
		"memory": func(b *Budget) { b.MemoryBytes = 150 }, "cpu": func(b *Budget) { b.CPUSlots = 1 },
		"scratch": func(b *Budget) { b.ScratchBytes = 150 }, "candidate": func(b *Budget) { b.CandidateBytes = 150 },
		"queue": func(b *Budget) { b.QueueBytes = 150 }, "worker": func(b *Budget) { b.Workers["format"] = 1 }, "concurrency": func(b *Budget) { b.MaxRunning = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			b := budget()
			alter(&b)
			q, gate := queue(t, b, &fakeRunner{stage: kernel.StageExport, claim: claim()})
			submit(t, q, job("one", kernel.StageExport, kernel.Background))
			submit(t, q, job("two", kernel.StageExport, kernel.Background))
			first := take(t, q)
			if second, reason := q.Take(); second != nil || reason != WaitCapacity {
				t.Fatalf("overcommitted %s: %v %s", name, second, reason)
			}
			if _, err := gate.Acquire(netgate.Network); err == nil {
				t.Fatal("network started during processing")
			}
			first.Finish()
			second := take(t, q)
			second.Finish()
			if q.State().Reserved != (kernel.ResourceClaim{}) || q.State().Running != 0 || len(gate.State()) != 0 {
				t.Fatal("reservation/lease leak")
			}
		})
	}
}

func TestQueueDoesNotStartSmallerBackgroundWorkWhileInteractiveWaits(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: kernel.ResourceClaim{MemoryBytes: 600}}, &fakeRunner{stage: kernel.StageValidate, claim: kernel.ResourceClaim{MemoryBytes: 100}})
	submit(t, q, job("active-background", kernel.StageExport, kernel.Background))
	active := take(t, q)
	submit(t, q, job("interactive", kernel.StageExport, kernel.Interactive))
	submit(t, q, job("small", kernel.StageValidate, kernel.Background))
	if ticket, reason := q.Take(); ticket != nil || reason != WaitCapacity {
		t.Fatal("background bypassed pending interactive work")
	}
	active.Finish()
	interactive := take(t, q)
	if interactive.Job().Attempt.JobID != "interactive" {
		t.Fatal("interactive not first")
	}
	interactive.Finish()
}

func TestQueueStorageHighWaterStillAllowsDownstreamValidationAndPublish(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect}, &fakeRunner{stage: kernel.StageExport}, &fakeRunner{stage: kernel.StageValidate}, &fakeRunner{stage: kernel.StagePublish})
	q.StorageBytes(850)
	for _, s := range []kernel.StageKind{kernel.StageExtractDetect, kernel.StageExport, kernel.StageValidate, kernel.StagePublish} {
		submit(t, q, job(string(s), s, kernel.Background))
	}
	for _, want := range []kernel.StageKind{kernel.StagePublish, kernel.StageValidate} {
		ticket := take(t, q)
		if ticket.Job().Stage != want {
			t.Fatal("wrong downstream order")
		}
		ticket.Finish()
	}
	if ticket, reason := q.Take(); ticket != nil || reason != WaitStorage {
		t.Fatalf("storage high water ignored: %v %s", ticket, reason)
	}
	q.StorageBytes(0)
	ticket := take(t, q)
	ticket.Finish()
}

func TestQueueRetainedStorageNotReleasedWhenStageEnds(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: kernel.ResourceClaim{CandidateBytes: 600}})
	submit(t, q, job("one", kernel.StageExport, kernel.Background))
	first := take(t, q)
	q.StorageBytes(600)
	first.Finish()
	submit(t, q, job("two", kernel.StageExport, kernel.Background))
	if ticket, reason := q.Take(); ticket != nil || reason != WaitCapacity {
		t.Fatal("retained candidate disk was treated as freed")
	}
	q.StorageBytes(0)
	second := take(t, q)
	second.Finish()
}

func TestQueuePausePressureActivityAndInteractiveReserve(t *testing.T) {
	b := budget()
	b.InteractiveMemoryReserve = 450
	q, gate := queue(t, b, &fakeRunner{stage: kernel.StageExport, claim: kernel.ResourceClaim{MemoryBytes: 600}})
	submit(t, q, job("background", kernel.StageExport, kernel.Background))
	if ticket, _ := q.Take(); ticket != nil {
		t.Fatal("background consumed interactive headroom")
	}
	submit(t, q, job("interactive", kernel.StageExport, kernel.Interactive))
	q.PauseDispatch(true)
	if _, r := q.Take(); r != WaitPaused {
		t.Fatal(r)
	}
	q.PauseDispatch(false)
	q.Pressure(true)
	if _, r := q.Take(); r != WaitPressure {
		t.Fatal(r)
	}
	q.Pressure(false)
	network, _ := gate.Acquire(netgate.Network)
	if _, r := q.Take(); r != WaitActivity {
		t.Fatal(r)
	}
	network.Release()
	ticket := take(t, q)
	if ticket.Job().Priority != kernel.Interactive {
		t.Fatal("reserve unavailable to interactive")
	}
	ticket.Finish()
}

func TestQueueRejectsInvalidAndDuplicateWork(t *testing.T) {
	b := budget()
	b.MaxQueued = 1
	q, _ := queue(t, b, &fakeRunner{stage: kernel.StageExport, claim: claim()})
	j := job("j", kernel.StageExport, kernel.Background)
	submit(t, q, j)
	if err := q.Submit(j); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if err := q.Submit(job("other", kernel.StageExport, kernel.Background)); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	if !q.RemovePending(j.Attempt) || q.RemovePending(j.Attempt) {
		t.Fatal("pending cancellation failed")
	}
	submit(t, q, j)
	ticket := take(t, q)
	if q.RemovePending(j.Attempt) {
		t.Fatal("running reservation removed before worker stopped")
	}
	if err := ticket.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := ticket.Finish(); !errors.Is(err, ErrTicket) {
		t.Fatal("double finish released resources twice")
	}
	invalid := j
	invalid.Priority = "urgent"
	if err := q.Submit(invalid); !errors.Is(err, ErrClaim) {
		t.Fatal("invalid priority accepted")
	}
	if err := q.Submit(job("missing", kernel.StageValidate, kernel.Background)); !errors.Is(err, ErrRunner) {
		t.Fatal("unregistered stage accepted")
	}
}

func TestQueueRunnerPanicUnknownOutcomeAndCancellationFailClosed(t *testing.T) {
	for name, run := range map[string]func(StageContext) StageOutcome{
		"panic":   func(StageContext) StageOutcome { panic("TEST_SECRET") },
		"unknown": func(StageContext) StageOutcome { return StageOutcome{Kind: "unknown"} },
	} {
		t.Run(name, func(t *testing.T) {
			q, gate := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: claim(), run: run})
			submit(t, q, job("j", kernel.StageExport, kernel.Background))
			ticket := take(t, q)
			out, err := ticket.Run(context.Background())
			if err != nil || out.Kind != Failed || out.Err == nil {
				t.Fatalf("unsafe result: %+v %v", out, err)
			}
			if q.State().Running != 0 || len(gate.State()) != 0 {
				t.Fatal("failed runner leaked reservation")
			}
		})
	}
	called := false
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, run: func(StageContext) StageOutcome { called = true; return StageOutcome{Kind: Completed} }})
	submit(t, q, job("cancelled", kernel.StageExport, kernel.Interactive))
	ticket := take(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := ticket.Run(ctx)
	if err != nil || out.Kind != Cancelled || called {
		t.Fatal("cancelled stage started")
	}
}

func TestQueueRunningTicketCannotRunTwiceOrReleaseEarly(t *testing.T) {
	entered, stop := make(chan struct{}), make(chan struct{})
	q, gate := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: claim(), run: func(StageContext) StageOutcome {
		close(entered)
		<-stop
		return StageOutcome{Kind: Completed}
	}})
	submit(t, q, job("j", kernel.StageExport, kernel.Background))
	ticket := take(t, q)
	finished := make(chan error, 1)
	go func() { _, err := ticket.Run(context.Background()); finished <- err }()
	<-entered
	if _, err := ticket.Run(context.Background()); !errors.Is(err, ErrTicket) {
		t.Fatal("same stage ran twice")
	}
	if err := ticket.Finish(); !errors.Is(err, ErrTicket) {
		t.Fatal("active stage released reservation before worker completion")
	}
	if q.State().Running != 1 || gate.State()[netgate.Processing] != 1 {
		t.Fatal("running stage lost lease")
	}
	close(stop)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if q.State().Running != 0 || len(gate.State()) != 0 {
		t.Fatal("completed stage retained lease")
	}
}

func TestQueueClaimCannotExceedBudgetOrUseUnknownWorker(t *testing.T) {
	for i, c := range []kernel.ResourceClaim{{MemoryBytes: -1}, {MemoryBytes: 1001}, {WorkerKind: "unregistered"}, {CPUSlots: 5}, {ScratchBytes: -1}, {QueueBytes: 1001}} {
		q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: c})
		if err := q.Submit(job(fmt.Sprint(i), kernel.StageExport, kernel.Background)); !errors.Is(err, ErrClaim) {
			t.Fatalf("invalid claim accepted: %+v", c)
		}
	}
}

func inGroup(group, id string, stage kernel.StageKind, priority kernel.Priority) JobView {
	j := job(id, stage, priority)
	j.Group = group
	return j
}

func takeOrder(t *testing.T, q *Queue, n int) []string {
	t.Helper()
	var got []string
	for range n {
		ticket := take(t, q)
		got = append(got, ticket.Job().Attempt.JobID)
		if err := ticket.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func TestQueueTakesTurnsBetweenBatchesSoALargeOneDoesNotKeepASmallOneWaiting(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect})
	for i := range 5 {
		submit(t, q, inGroup("large", fmt.Sprintf("l%d", i), kernel.StageExtractDetect, kernel.Background))
	}
	submit(t, q, inGroup("small", "s0", kernel.StageExtractDetect, kernel.Background))
	submit(t, q, inGroup("small", "s1", kernel.StageExtractDetect, kernel.Background))
	got := fmt.Sprint(takeOrder(t, q, 7))
	if want := "[l0 s0 l1 s1 l2 l3 l4]"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
}

// Turns are between batches: inside one, the stage closest to delivery still goes first.
func TestQueueKeepsDownstreamFirstWithinABatch(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect}, &fakeRunner{stage: kernel.StagePublish})
	submit(t, q, inGroup("a", "a-extract", kernel.StageExtractDetect, kernel.Background))
	submit(t, q, inGroup("a", "a-publish", kernel.StagePublish, kernel.Background))
	submit(t, q, inGroup("b", "b-extract", kernel.StageExtractDetect, kernel.Background))
	got := fmt.Sprint(takeOrder(t, q, 3))
	if want := "[a-publish b-extract a-extract]"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
}

// A new stage of a batch that just had its turn does not jump ahead of the batch waiting for one.
func TestQueueAStageSubmittedLaterDoesNotTakeAnotherBatchsTurn(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect}, &fakeRunner{stage: kernel.StageResolve})
	submit(t, q, inGroup("a", "a1", kernel.StageExtractDetect, kernel.Background))
	submit(t, q, inGroup("b", "b1", kernel.StageExtractDetect, kernel.Background))
	first := take(t, q)
	if first.Job().Attempt.JobID != "a1" {
		t.Fatalf("first %s", first.Job().Attempt.JobID)
	}
	first.Finish()
	submit(t, q, inGroup("a", "a2", kernel.StageResolve, kernel.Background))
	if got := fmt.Sprint(takeOrder(t, q, 2)); got != "[b1 a2]" {
		t.Fatalf("order %s", got)
	}
}

// Priority comes before turns: however many batches wait and however long, the interactive stage is next.
func TestQueueInteractiveWorkGoesAheadOfEveryBatchsTurn(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect}, &fakeRunner{stage: kernel.StagePreflight})
	submit(t, q, inGroup("a", "a1", kernel.StageExtractDetect, kernel.Background))
	submit(t, q, inGroup("b", "b1", kernel.StageExtractDetect, kernel.Background))
	submit(t, q, inGroup("quick", "q1", kernel.StagePreflight, kernel.Interactive))
	if got := fmt.Sprint(takeOrder(t, q, 3)); got != "[q1 a1 b1]" {
		t.Fatalf("order %s", got)
	}
}

// A request that does not fit gives way to the next group that does, and keeps its place for when it fits.
func TestQueueTurnsSkipWhatDoesNotFit(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: kernel.ResourceClaim{MemoryBytes: 950}}, &fakeRunner{stage: kernel.StageValidate, claim: kernel.ResourceClaim{MemoryBytes: 100}})
	submit(t, q, inGroup("x", "active", kernel.StageValidate, kernel.Background))
	active := take(t, q)
	submit(t, q, inGroup("a", "big", kernel.StageExport, kernel.Background))
	submit(t, q, inGroup("b", "small", kernel.StageValidate, kernel.Background))
	next := take(t, q)
	if next.Job().Attempt.JobID != "small" {
		t.Fatalf("next %s", next.Job().Attempt.JobID)
	}
	active.Finish()
	next.Finish()
	if got := take(t, q).Job().Attempt.JobID; got != "big" {
		t.Fatalf("then %s", got)
	}
}

// What was served is kept only while something is waiting or running: a long session does not grow it.
func TestQueueForgetsTurnsOnceNothingIsInIt(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect})
	submit(t, q, inGroup("a", "a1", kernel.StageExtractDetect, kernel.Background))
	running := take(t, q)
	submit(t, q, inGroup("b", "b1", kernel.StageExtractDetect, kernel.Background)) // with one running the record stays
	if len(q.turns) != 2 {
		t.Fatalf("%d groups recorded while one ran", len(q.turns))
	}
	running.Finish()
	take(t, q).Finish()
	submit(t, q, inGroup("c", "c1", kernel.StageExtractDetect, kernel.Background))
	if _, kept := q.turns["a"]; kept || len(q.turns) != 1 {
		t.Fatalf("kept after the queue emptied: %v", q.turns)
	}
}

package sched

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/netgate"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher failed to make progress")
		var zero T
		return zero
	}
}

func TestDispatcherWakesOnResumeAndPersistsDownstreamOutcomes(t *testing.T) {
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExtractDetect}, &fakeRunner{stage: kernel.StageResolve})
	q.PauseDispatch(true)
	j := job("j", kernel.StageExtractDetect, kernel.Background)
	submit(t, q, j)
	d := NewDispatcher(q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan JobView, 2)
	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx, func(view JobView, out StageOutcome) error {
			completed <- view
			if view.Stage == kernel.StageExtractDetect {
				next := job("j2", kernel.StageResolve, kernel.Background)
				return q.Submit(next)
			}
			cancel()
			return nil
		})
	}()
	q.PauseDispatch(false)
	if first := receive(t, completed); first.Stage != kernel.StageExtractDetect {
		t.Fatal("wrong first stage")
	}
	if next := receive(t, completed); next.Stage != kernel.StageResolve {
		t.Fatal("completion did not wake downstream work")
	}
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if q.State().Running != 0 {
		t.Fatal("dispatcher leaked reservations")
	}
}

func TestDispatcherCancelsActiveStageWithoutReleasingBeforeReturn(t *testing.T) {
	entered, cancelled, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	q, gate := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: claim(), run: func(ctx StageContext) StageOutcome {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-exit
		return StageOutcome{Kind: Cancelled}
	}})
	j := job("j", kernel.StageExport, kernel.Background)
	submit(t, q, j)
	d := NewDispatcher(q)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx, func(_ JobView, out StageOutcome) error {
			if out.Kind != Cancelled {
				t.Error("cancelled runner reported success")
			}
			stop()
			return nil
		})
	}()
	receive(t, entered)
	if !d.Cancel(j.Attempt) {
		t.Fatal("running attempt cancellation lost")
	}
	receive(t, cancelled)
	if q.State().Running != 1 || gate.State()[netgate.Processing] != 1 {
		t.Fatal("cancel request prematurely freed resources")
	}
	close(exit)
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if q.State().Running != 0 || len(gate.State()) != 0 {
		t.Fatal("returned runner leaked resources")
	}
}

func TestDispatcherCompletionFailureStopsAndDrainsOtherStages(t *testing.T) {
	bothEntered := make(chan struct{}, 2)
	release := make(chan struct{})
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, run: func(ctx StageContext) StageOutcome {
		bothEntered <- struct{}{}
		select {
		case <-release:
			return StageOutcome{Kind: Completed}
		case <-ctx.Done():
			return StageOutcome{Kind: Cancelled}
		}
	}})
	submit(t, q, job("one", kernel.StageExport, kernel.Background))
	submit(t, q, job("two", kernel.StageExport, kernel.Background))
	d := NewDispatcher(q)
	failure := errors.New("fixture commit failed")
	done := make(chan error, 1)
	callbacks := make(chan struct{}, 2)
	go func() {
		done <- d.Run(context.Background(), func(JobView, StageOutcome) error { callbacks <- struct{}{}; return failure })
	}()
	receive(t, bothEntered)
	receive(t, bothEntered)
	close(release)
	if err := receive(t, done); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(callbacks) != 2 || q.State().Running != 0 || !q.State().Paused {
		t.Fatal("failed completion did not drain and stop")
	}
}

// A stage refused because a download holds the network lease runs once the lease is released, with nothing else
// happening: the dispatcher is asleep by then and only the release can wake it (#43).
func TestAStageRefusedForALeaseRunsOnceTheLeaseIsReleased(t *testing.T) {
	for _, kind := range []netgate.LeaseKind{netgate.Network, netgate.Maintenance} {
		t.Run(string(kind), func(t *testing.T) {
			q, gate := queue(t, budget(), &fakeRunner{stage: kernel.StageExport})
			lease, err := gate.Acquire(kind)
			if err != nil {
				t.Fatal(err)
			}
			submit(t, q, job("j", kernel.StageExport, kernel.Interactive))
			<-q.wake // what the submit signalled is spent: the dispatcher's first look finds the lease held
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := NewDispatcher(q)
			asleep := make(chan struct{}, 1)
			d.beforeWait = func() {
				select {
				case asleep <- struct{}{}:
				default:
				}
			}
			ran := make(chan JobView, 1)
			done := make(chan error, 1)
			go func() {
				done <- d.Run(ctx, func(view JobView, _ StageOutcome) error {
					ran <- view
					cancel()
					return nil
				})
			}()
			receive(t, asleep)
			if s := q.State(); s.Pending != 1 || s.Running != 0 {
				t.Fatalf("the stage ran while the lease was held: %+v", s)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			if view := receive(t, ran); view.Attempt.JobID != "j" {
				t.Fatalf("%+v", view)
			}
			if err := receive(t, done); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

// pipeline runs jobs through a dispatcher on one slot, as the comfortable tier does. A file's next stage is submitted
// from the completion of the last, so the order is decided by the queue alone. It returns what ran, in order.
func pipeline(t *testing.T, before func(q *Queue), next func(q *Queue, done JobView) error, want int) []string {
	t.Helper()
	b := budget()
	b.MaxRunning, b.MaxQueued = 1, 2000
	stages := []kernel.StageKind{kernel.StagePreflight, kernel.StageExtractDetect, kernel.StageResolve, kernel.StageExport, kernel.StageValidate, kernel.StagePublish}
	runners := make([]StageRunner, len(stages))
	for i, s := range stages {
		runners[i] = &fakeRunner{stage: s}
	}
	q, _ := queue(t, b, runners...)
	before(q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var order []string
	finished := make(chan struct{})
	done := make(chan error, 1)
	d := NewDispatcher(q)
	go func() {
		done <- d.Run(ctx, func(view JobView, _ StageOutcome) error {
			order = append(order, view.Attempt.JobID)
			if len(order) == want {
				close(finished)
			}
			return next(q, view)
		})
	}()
	receive(t, finished)
	cancel()
	receive(t, done)
	return order
}

// nextStage is what follows in a file's pipeline, as the same job under a new attempt; "" at the end of it.
func nextStage(s kernel.StageKind) kernel.StageKind {
	switch s {
	case kernel.StagePreflight:
		return kernel.StageExtractDetect
	case kernel.StageExtractDetect:
		return kernel.StageResolve
	case kernel.StageResolve:
		return kernel.StageExport
	case kernel.StageExport:
		return kernel.StageValidate
	case kernel.StageValidate:
		return kernel.StagePublish
	}
	return ""
}

func follow(q *Queue, done JobView) error {
	stage := nextStage(done.Stage)
	if stage == "" {
		return nil
	}
	j := done
	j.Stage = stage
	j.Attempt.AttemptID += "+"
	j.Attempt.JobVersion++
	return q.Submit(j)
}

func position(order []string, pred func(string) bool) (first, last int) {
	first = -1
	for i, id := range order {
		if pred(id) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return first, last
}

// A file of one batch beside 200 files of another: each is one of two turns, so it is through after at most twice its
// own stages, however large the other batch is. Without turns it waits behind all 200 extractions.
func TestALargeBatchDoesNotStarveASmallOne(t *testing.T) {
	small := func(id string) bool { return id == "small" }
	order := pipeline(t, func(q *Queue) {
		for i := range 200 {
			submit(t, q, inGroup("large", fmt.Sprintf("l%03d", i), kernel.StageExtractDetect, kernel.Background))
		}
		submit(t, q, inGroup("small", "small", kernel.StageExtractDetect, kernel.Background))
	}, follow, 40)
	first, last := position(order, small)
	if first != 1 || last != 9 { // its 5 stages, each after one stage of the other batch
		t.Fatalf("the small batch ran from %d to %d of %v, want 1 to 9", first, last, order)
	}
}

// A quick file arriving while a batch runs: no further stage of the batch starts until the file is through. The stage
// that ran as it came is not interrupted, and what was done before stays done.
func TestALargeBatchDoesNotStarveAnInteractiveFile(t *testing.T) {
	quick := func(id string) bool { return id == "quick" }
	const before = 10
	seen := 0
	order := pipeline(t, func(q *Queue) {
		for i := range 200 {
			submit(t, q, inGroup("large", fmt.Sprintf("l%03d", i), kernel.StagePublish, kernel.Background))
		}
	}, func(q *Queue, done JobView) error {
		if done.Group == "large" {
			if seen++; seen == before {
				return q.Submit(inGroup("single", "quick", kernel.StagePreflight, kernel.Interactive))
			}
		}
		return follow(q, done)
	}, before+6+5)
	first, last := position(order, quick)
	if first != before || last != before+5 {
		t.Fatalf("the file ran from %d to %d of %v, want %d to %d", first, last, order, before, before+5)
	}
}

// A stage counts as running until the dispatcher lets go of it, which is just before its completion: a completion
// looking at its own job sees nothing left of it.
func TestRunningCoversAStageUntilItsCompletion(t *testing.T) {
	entered, exit := make(chan struct{}), make(chan struct{})
	q, _ := queue(t, budget(), &fakeRunner{stage: kernel.StageExport, claim: claim(), run: func(StageContext) StageOutcome {
		close(entered)
		<-exit
		return StageOutcome{Kind: Completed}
	}})
	j := job("j", kernel.StageExport, kernel.Background)
	submit(t, q, j)
	d := NewDispatcher(q)
	if d.Running(j.Attempt.JobID) {
		t.Fatal("a queued stage counts as running")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	inCompletion := make(chan bool, 1)
	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx, func(v JobView, _ StageOutcome) error {
			inCompletion <- d.Running(v.Attempt.JobID)
			stop()
			return nil
		})
	}()
	receive(t, entered)
	if !d.Running(j.Attempt.JobID) {
		t.Fatal("a running stage does not count as running")
	}
	if d.Running("another") {
		t.Fatal("another job counts as running")
	}
	close(exit)
	if receive(t, inCompletion) {
		t.Fatal("a completion sees its own stage as running")
	}
	receive(t, done)
}

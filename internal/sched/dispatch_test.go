package sched

import (
	"context"
	"errors"
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

func TestDispatcherExternalLeaseReleaseNeedsWake(t *testing.T) {
	q, gate := queue(t, budget(), &fakeRunner{stage: kernel.StageExport})
	lease, _ := gate.Acquire(netgate.Network)
	submit(t, q, job("j", kernel.StageExport, kernel.Interactive))
	if _, reason := q.Take(); reason != WaitActivity {
		t.Fatal(reason)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- NewDispatcher(q).Run(ctx, func(JobView, StageOutcome) error { cancel(); return nil }) }()
	lease.Release()
	q.Wake()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

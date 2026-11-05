package sched

import (
	"context"
	"errors"
	"sync"

	"github.com/jorben/tacet/internal/kernel"
)

var ErrDispatcherRunning = errors.New("SCHED_DISPATCHER_ALREADY_RUNNING")

// Completion persists a result under workspace admission. It is called serially
// after the runner returns, and may submit downstream work. Its error stops new
// dispatch, cancels active stages and drains their results before Run returns.
// It must not perform plugin I/O. The caller supplies the commit context: runner
// cancellation must not prevent saving the final cancellation/checkpoint state.
type Completion func(JobView, StageOutcome) error

type stageResult struct {
	job     JobView
	outcome StageOutcome
	err     error
}

// Dispatcher drives the queue by event notifications. It does not own job state
// or database connections, and never imports a stage implementation. A runner
// must stop its entire process tree before returning from cancellation.
type Dispatcher struct {
	queue   *Queue
	mu      sync.Mutex
	running bool
	active  map[kernel.AttemptRef]context.CancelFunc
}

func NewDispatcher(q *Queue) *Dispatcher {
	return &Dispatcher{queue: q, active: map[kernel.AttemptRef]context.CancelFunc{}}
}

// Cancel removes queued work or signals the matching running stage. It does
// not release resource reservations; those survive until the runner actually
// stops. The jobs command that requested cancellation owns the durable state.
func (d *Dispatcher) Cancel(a kernel.AttemptRef) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel, ok := d.active[a]; ok {
		cancel()
		return true
	}
	return d.queue.RemovePending(a)
}

// Running reports whether a stage of the job was taken from the queue and its completion has not been called yet.
// The stage is let go of before that call, so a completion does not see its own stage here.
func (d *Dispatcher) Running(job string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for a := range d.active {
		if a.JobID == job {
			return true
		}
	}
	return false
}

// Run serves until ctx cancellation or a completion error. Paused, pressured,
// high-water or activity-blocked queues wait for Wake/state notifications, not
// timers. Only one dispatcher can own a queue through this instance.
func (d *Dispatcher) Run(ctx context.Context, complete Completion) error {
	if d == nil || d.queue == nil || complete == nil {
		return ErrRunner
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return ErrDispatcherRunning
	}
	d.running = true
	d.mu.Unlock()
	defer func() { d.mu.Lock(); d.running = false; d.mu.Unlock() }()
	results := make(chan stageResult, d.queue.budget.MaxRunning)
	var failure error
	stopping := false
	stop := func(err error) {
		if !stopping {
			stopping = true
			failure = err
			d.queue.PauseDispatch(true)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, cancel := range d.active {
			cancel()
		}
	}
	for {
		if ctx.Err() != nil && !stopping {
			stop(ctx.Err())
		}
		if !stopping {
			for {
				// Cancel and admission registration use the same lock, so a
				// command cannot miss a ticket between Take and execution.
				d.mu.Lock()
				// Completed executions still count until their outcome is consumed;
				// otherwise wake notifications could spawn an unbounded backlog.
				if len(d.active) >= d.queue.budget.MaxRunning {
					d.mu.Unlock()
					break
				}
				ticket, _ := d.queue.Take()
				if ticket == nil {
					d.mu.Unlock()
					break
				}
				stageCtx, cancel := context.WithCancel(ctx)
				job := ticket.Job()
				d.active[job.Attempt] = cancel
				d.mu.Unlock()
				go func() {
					outcome, err := ticket.Run(stageCtx)
					results <- stageResult{job: job, outcome: outcome, err: err}
				}()
			}
		}
		d.mu.Lock()
		activeCount := len(d.active)
		d.mu.Unlock()
		if stopping && activeCount == 0 {
			return failure
		}
		// A stopped context is removed from select after the first stop,
		// avoiding a busy loop while uncooperative stages are being reclaimed.
		var cancelled <-chan struct{}
		if !stopping {
			cancelled = ctx.Done()
		}
		select {
		case <-cancelled:
			stop(ctx.Err())
		case <-d.queue.wake:
		case result := <-results:
			d.mu.Lock()
			cancel := d.active[result.job.Attempt]
			delete(d.active, result.job.Attempt)
			d.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if result.err != nil {
				stop(result.err)
			}
			if err := complete(result.job, result.outcome); err != nil {
				stop(err)
			}
		}
	}
}

package sched

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/netgate"
)

var (
	ErrBudget    = errors.New("SCHED_BUDGET_INVALID")
	ErrClaim     = errors.New("SCHED_CLAIM_INVALID")
	ErrQueueFull = errors.New("SCHED_QUEUE_FULL")
	ErrDuplicate = errors.New("SCHED_ATTEMPT_DUPLICATE")
	ErrRunner    = errors.New("SCHED_RUNNER_UNREGISTERED")
	ErrTicket    = errors.New("SCHED_TICKET_RELEASED")
)

// Budget is the stage reservation envelope after the application has deducted
// UI/controller and resident model/cache overhead. Real process-tree monitoring
// must still report pressure; a plugin's declared claim alone is not a memory cap.
type Budget struct {
	MemoryBytes, ScratchBytes, CandidateBytes, QueueBytes int64
	CPUSlots, MaxRunning, MaxQueued                       int
	Workers                                               map[string]int
	InteractiveMemoryReserve                              int64
	// HighWaterBytes applies to all retained batch data, including checkpoints,
	// decrypted input and candidates. Zero disables the extra high-water test.
	HighWaterBytes int64
}

type WaitReason string

const (
	WaitIdle     WaitReason = "idle"
	WaitPaused   WaitReason = "dispatch_paused"
	WaitPressure WaitReason = "resource_pressure"
	WaitStorage  WaitReason = "storage_high_water"
	WaitCapacity WaitReason = "resource_capacity"
	WaitActivity WaitReason = "activity_busy"
)

type reservation struct {
	job   JobView
	claim kernel.ResourceClaim
	order uint64
}

// Queue keeps only a bounded set of stage views/claims in memory. jobs remains
// the durable source; after restart pending stages are reconstructed from it.
// No stage modules are imported and no plugin is started by admission itself.
type Queue struct {
	mu               sync.Mutex
	budget           Budget
	gate             *netgate.Gate
	runners          map[kernel.StageKind]StageRunner
	pending          []reservation
	active           map[kernel.AttemptRef]*Ticket
	known            map[kernel.AttemptRef]bool
	workers          map[string]int
	used             kernel.ResourceClaim
	order            uint64
	paused, pressure bool
	storedBytes      int64
	wake             chan struct{}
}

func NewQueue(b Budget, gate *netgate.Gate, runners ...StageRunner) (*Queue, error) {
	if gate == nil || b.MemoryBytes <= 0 || b.ScratchBytes <= 0 || b.CandidateBytes <= 0 || b.QueueBytes <= 0 || b.CPUSlots <= 0 || b.MaxRunning <= 0 || b.MaxQueued <= 0 || b.InteractiveMemoryReserve < 0 || b.InteractiveMemoryReserve > b.MemoryBytes || b.HighWaterBytes < 0 {
		return nil, ErrBudget
	}
	limits := make(map[string]int, len(b.Workers))
	for kind, limit := range b.Workers {
		if kind == "" || limit <= 0 {
			return nil, ErrBudget
		}
		limits[kind] = limit
	}
	b.Workers = limits
	q := &Queue{budget: b, gate: gate, runners: map[kernel.StageKind]StageRunner{}, active: map[kernel.AttemptRef]*Ticket{}, known: map[kernel.AttemptRef]bool{}, workers: map[string]int{}, wake: make(chan struct{}, 1)}
	for _, runner := range runners {
		if runner == nil || !validStage(runner.Kind()) {
			return nil, ErrRunner
		}
		if _, exists := q.runners[runner.Kind()]; exists {
			return nil, ErrRunner
		}
		q.runners[runner.Kind()] = runner
	}
	return q, nil
}

func validStage(s kernel.StageKind) bool {
	switch s {
	case kernel.StagePreflight, kernel.StageUnlock, kernel.StageExtractDetect, kernel.StageSupplementScan, kernel.StageResolve, kernel.StageExport, kernel.StageValidate, kernel.StagePublish:
		return true
	}
	return false
}

func validJob(j JobView) bool {
	a := j.Attempt
	return a.JobID != "" && a.AttemptID != "" && len(a.JobID) <= 128 && len(a.AttemptID) <= 128 && a.JobVersion > 0 && a.OwnerGeneration > 0 && (j.Priority == kernel.Background || j.Priority == kernel.Interactive)
}

func (q *Queue) validClaim(c kernel.ResourceClaim) bool {
	if c.MemoryBytes < 0 || c.ScratchBytes < 0 || c.CandidateBytes < 0 || c.QueueBytes < 0 || c.CPUSlots < 0 {
		return false
	}
	if c.WorkerKind != "" && q.budget.Workers[c.WorkerKind] == 0 {
		return false
	}
	return c.MemoryBytes <= q.budget.MemoryBytes && c.ScratchBytes <= q.budget.ScratchBytes && c.CandidateBytes <= q.budget.CandidateBytes && c.QueueBytes <= q.budget.QueueBytes && c.CPUSlots <= q.budget.CPUSlots
}

// Submit returns promptly and never silently evicts another file. Claims are
// calculated outside the queue lock; Claim must be a bounded, non-I/O estimate.
func (q *Queue) Submit(job JobView) error {
	if !validJob(job) {
		return ErrClaim
	}
	runner, ok := q.runners[job.Stage]
	if !ok {
		return ErrRunner
	}
	claim := runner.Claim(job)
	claim.Priority = job.Priority // entry metadata, not a plugin, sets priority
	if !q.validClaim(claim) {
		return ErrClaim
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.known[job.Attempt] {
		return ErrDuplicate
	}
	if len(q.pending) >= q.budget.MaxQueued {
		return ErrQueueFull
	}
	q.order++
	q.pending = append(q.pending, reservation{job: job, claim: claim, order: q.order})
	q.known[job.Attempt] = true
	q.signal()
	return nil
}

func stageRank(stage kernel.StageKind) int {
	switch stage {
	case kernel.StagePublish:
		return 8
	case kernel.StageValidate:
		return 7
	case kernel.StageExport:
		return 6
	case kernel.StageResolve:
		return 5
	case kernel.StageUnlock:
		return 4
	case kernel.StageSupplementScan:
		return 3
	case kernel.StageExtractDetect:
		return 2
	case kernel.StagePreflight:
		return 1
	}
	return 0
}

func growsStorage(stage kernel.StageKind) bool {
	return stage == kernel.StageExtractDetect || stage == kernel.StageSupplementScan || stage == kernel.StageExport || stage == kernel.StageUnlock
}

func (q *Queue) fits(c kernel.ResourceClaim, priority kernel.Priority) bool {
	b := q.budget
	memory := b.MemoryBytes
	if priority == kernel.Background {
		memory -= b.InteractiveMemoryReserve
	}
	if c.MemoryBytes > memory-q.used.MemoryBytes || c.CPUSlots > b.CPUSlots-q.used.CPUSlots || c.ScratchBytes > b.ScratchBytes-q.used.ScratchBytes || c.QueueBytes > b.QueueBytes-q.used.QueueBytes {
		return false
	}
	// Retained disk is separate from executing-stage reservation; finishing a
	// worker does not mean its awaiting-review candidate has disappeared.
	if q.storedBytes > b.CandidateBytes || c.CandidateBytes > b.CandidateBytes-q.storedBytes-q.used.CandidateBytes {
		return false
	}
	return c.WorkerKind == "" || q.workers[c.WorkerKind] < b.Workers[c.WorkerKind]
}

// Ticket is one admitted stage. Its processing lease spans execution, including
// cancellation/reclamation, and is released only by Finish. Claim reservations
// are not released merely because a cancellation request was submitted.
type Ticket struct {
	queue     *Queue
	request   reservation
	lease     *netgate.Lease
	released  bool
	executing bool
}

func (t *Ticket) Job() JobView                { return t.request.job }
func (t *Ticket) Claim() kernel.ResourceClaim { return t.request.claim }

// Take applies strict interactive priority, then downstream-first ordering and
// FIFO member order within a stage. Active background work is never preempted.
// When interactive work is pending, no new background stage may be admitted,
// even if only a smaller background claim happens to fit.
func (q *Queue) Take() (*Ticket, WaitReason) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil, WaitIdle
	}
	if q.paused {
		return nil, WaitPaused
	}
	if q.pressure {
		return nil, WaitPressure
	}
	if len(q.active) >= q.budget.MaxRunning {
		return nil, WaitCapacity
	}
	interactive := false
	for _, request := range q.pending {
		if request.job.Priority == kernel.Interactive {
			interactive = true
			break
		}
	}
	indexes := make([]int, 0, len(q.pending))
	for i, request := range q.pending {
		if !interactive || request.job.Priority == kernel.Interactive {
			indexes = append(indexes, i)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, b := q.pending[indexes[i]], q.pending[indexes[j]]
		if stageRank(a.job.Stage) != stageRank(b.job.Stage) {
			return stageRank(a.job.Stage) > stageRank(b.job.Stage)
		}
		return a.order < b.order
	})
	reason := WaitCapacity
	for _, index := range indexes {
		request := q.pending[index]
		if q.budget.HighWaterBytes > 0 && q.storedBytes >= q.budget.HighWaterBytes && growsStorage(request.job.Stage) {
			reason = WaitStorage
			continue
		}
		if !q.fits(request.claim, request.job.Priority) {
			continue
		}
		lease, err := q.gate.Acquire(netgate.Processing)
		if err != nil {
			return nil, WaitActivity
		}
		ticket := &Ticket{queue: q, request: request, lease: lease}
		q.active[request.job.Attempt] = ticket
		q.pending = append(q.pending[:index], q.pending[index+1:]...)
		q.reserve(request.claim, 1)
		return ticket, ""
	}
	return nil, reason
}

func (q *Queue) reserve(c kernel.ResourceClaim, direction int64) {
	q.used.MemoryBytes += direction * c.MemoryBytes
	q.used.ScratchBytes += direction * c.ScratchBytes
	q.used.CandidateBytes += direction * c.CandidateBytes
	q.used.QueueBytes += direction * c.QueueBytes
	q.used.CPUSlots += int(direction) * c.CPUSlots
	if c.WorkerKind != "" {
		q.workers[c.WorkerKind] += int(direction)
	}
}

func (t *Ticket) Finish() error {
	if t == nil || t.queue == nil {
		return ErrTicket
	}
	q := t.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.released || t.executing {
		return ErrTicket
	}
	return t.finishLocked()
}

func (t *Ticket) finishLocked() error {
	q := t.queue
	t.released = true
	delete(q.active, t.request.job.Attempt)
	delete(q.known, t.request.job.Attempt)
	q.reserve(t.request.claim, -1)
	q.signal()
	return t.lease.Release()
}

// RemovePending removes only queued work. A running ticket must first cancel
// its stage and reclaim the worker, then Finish; capacity cannot be reused early.
func (q *Queue) RemovePending(attempt kernel.AttemptRef) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, request := range q.pending {
		if request.job.Attempt == attempt {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			delete(q.known, attempt)
			q.signal()
			return true
		}
	}
	return false
}

// signal coalesces state changes; it never waits for a dispatcher. The queue
// state remains authoritative if several notifications collapse into one.
func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Wake notifies the dispatcher after an external condition changes, such as
// release of a network lease. There is no periodic admission polling.
func (q *Queue) Wake() { q.signal() }

func (q *Queue) PauseDispatch(paused bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = paused
	q.signal()
}
func (q *Queue) Pressure(pressure bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pressure = pressure
	q.signal()
}
func (q *Queue) StorageBytes(bytes int64) error {
	if bytes < 0 {
		return ErrBudget
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.storedBytes = bytes
	q.signal()
	return nil
}

type QueueState struct {
	Pending, Running int
	Reserved         kernel.ResourceClaim
	StoredBytes      int64
	Paused, Pressure bool
}

func (q *Queue) State() QueueState {
	q.mu.Lock()
	defer q.mu.Unlock()
	return QueueState{len(q.pending), len(q.active), q.used, q.storedBytes, q.paused, q.pressure}
}

type stageContext struct {
	context.Context
	attempt kernel.AttemptRef
}

func (c stageContext) Attempt() kernel.AttemptRef { return c.attempt }

// Run executes an admitted stage. The runner must honor context and wait for
// actual worker reclamation before returning. A panic/unknown result fails the
// stage, never masquerades as a completed zero-hit detection. The caller writes
// the result to jobs under workspace admission before requesting the next stage.
func (t *Ticket) Run(ctx context.Context) (out StageOutcome, err error) {
	if t == nil || t.queue == nil {
		return StageOutcome{}, ErrTicket
	}
	q := t.queue
	q.mu.Lock()
	if t.released || t.executing {
		q.mu.Unlock()
		return StageOutcome{}, ErrTicket
	}
	t.executing = true
	runner := q.runners[t.request.job.Stage]
	q.mu.Unlock()
	defer func() {
		if recover() != nil {
			out = StageOutcome{Kind: Failed, Err: &kernel.ErrorCode{Code: "STAGE_PANIC", Retry: kernel.RetryNone}}
		}
		switch out.Kind {
		case Completed, NeedsReview, Blocked, Retry, Failed, Cancelled:
		default:
			out = StageOutcome{Kind: Failed, Err: &kernel.ErrorCode{Code: "STAGE_OUTCOME_INVALID", Retry: kernel.RetryNone}}
		}
		q.mu.Lock()
		t.executing = false
		finishErr := t.finishLocked()
		q.mu.Unlock()
		if err == nil {
			err = finishErr
		}
	}()
	if ctx.Err() != nil {
		return StageOutcome{Kind: Cancelled}, nil
	}
	out = runner.Run(stageContext{Context: ctx, attempt: t.request.job.Attempt})
	if ctx.Err() != nil {
		out = StageOutcome{Kind: Cancelled}
	}
	return out, nil
}

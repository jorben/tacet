package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrConfig  = errors.New("LIFECYCLE_CONFIG_INVALID")
	ErrRunning = errors.New("LIFECYCLE_ALREADY_STARTED")
	ErrNotUp   = errors.New("LIFECYCLE_NOT_STARTED")
)

// Step is one named action of the start-up or exit order.
type Step struct {
	Name string
	Run  func(ctx context.Context) error
}

// StepError says which step stopped the order and why; the steps after it did not run.
type StepError struct {
	Step string
	Err  error
}

func (e *StepError) Error() string { return "LIFECYCLE_STEP_FAILED:" + e.Step }
func (e *StepError) Unwrap() error { return e.Err }

// The start-up order of module design 4.3, by name. Wiring supplies the actions.
const (
	StepWorkspace = "workspace"       // take the lock, raise the generation
	StepReap      = "reap"            // the previous generation's worker trees are gone
	StepPublish   = "publish"         // settle publish intents left prepared
	StepSweep     = "retention_sweep" // expired data and data without a recovery grant
	StepRecover   = "recover"         // drop pending bulk decisions, reset unfinished stages
	StepDispatch  = "dispatch"        // the scheduler starts, task operations open
)

// Status is what the "recovering" screen shows.
type Status struct {
	Step  string
	Done  bool
	Error error
}

// Starter runs the start-up order.
type Starter struct {
	steps   []Step
	notify  func(Status)
	mu      sync.Mutex
	started bool
	opened  bool
}

// NewStarter checks that the steps are exactly the designed order: a missing or
// swapped step would make recovery unsafe, so it is a configuration error.
func NewStarter(steps []Step, notify func(Status)) (*Starter, error) {
	want := []string{StepWorkspace, StepReap, StepPublish, StepSweep, StepRecover, StepDispatch}
	if len(steps) != len(want) {
		return nil, ErrConfig
	}
	for i, s := range steps {
		if s.Name != want[i] || s.Run == nil {
			return nil, ErrConfig
		}
	}
	return &Starter{steps: steps, notify: notify}, nil
}

func (s *Starter) emit(st Status) {
	if s.notify != nil {
		s.notify(st)
	}
}

// Start runs each step only after the previous one succeeded. The first failure
// stops the order, reports it and leaves task operations closed (Open() stays
// false), so nothing runs on a workspace that was not recovered.
func (s *Starter) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrRunning
	}
	s.started = true
	s.mu.Unlock()
	for _, st := range s.steps {
		s.emit(Status{Step: st.Name})
		if err := ctx.Err(); err != nil {
			s.emit(Status{Step: st.Name, Error: err})
			return &StepError{st.Name, err}
		}
		if err := st.Run(ctx); err != nil {
			s.emit(Status{Step: st.Name, Error: err})
			return &StepError{st.Name, err}
		}
		s.emit(Status{Step: st.Name, Done: true})
	}
	s.mu.Lock()
	s.opened = true
	s.mu.Unlock()
	return nil
}

// Open reports whether recovery finished and task operations may be accepted.
func (s *Starter) Open() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

// The exit order of architecture 6.2.
const (
	ExitStopDispatch = "stop_dispatch"
	ExitCancel       = "cancel_to_boundary"
	ExitCheckpoint   = "checkpoint"
	ExitReap         = "reap"
	ExitPublishWait  = "publish_wait"
	ExitRetention    = "retention"
	ExitRelease      = "release_workspace"
)

// ExitMode distinguishes a normal exit from the system shutting down.
type ExitMode int

const (
	Normal ExitMode = iota
	SystemShutdown
)

// Stopper runs the exit order.
type Stopper struct {
	steps  map[string]Step
	notify func(Status)
	// Grace is how long a normal exit waits for work to reach a safe boundary (PF-04).
	Grace time.Duration
	mu    sync.Mutex
	done  bool
}

func NewStopper(steps []Step, grace time.Duration, notify func(Status)) (*Stopper, error) {
	want := []string{ExitStopDispatch, ExitCancel, ExitCheckpoint, ExitReap, ExitPublishWait, ExitRetention, ExitRelease}
	if len(steps) != len(want) || grace <= 0 {
		return nil, ErrConfig
	}
	m := map[string]Step{}
	for i, s := range steps {
		if s.Name != want[i] || s.Run == nil {
			return nil, ErrConfig
		}
		m[s.Name] = s
	}
	return &Stopper{steps: m, Grace: grace, notify: notify}, nil
}

// Stop runs the exit order. Normal: every step, in order, none skipped because an
// earlier one failed (an exit has to get as far as it can); the workspace is released
// last, always, and a failed step is reported. The cancellation step is bounded by
// Grace, and what is not at a safe boundary then is reaped by the following steps.
// SystemShutdown skips waiting and cleaning: it stops dispatch and reaps processes,
// then releases the workspace. Stop is idempotent.
func (s *Stopper) Stop(ctx context.Context, mode ExitMode) error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	s.mu.Unlock()
	order := []string{ExitStopDispatch, ExitCancel, ExitCheckpoint, ExitReap, ExitPublishWait, ExitRetention, ExitRelease}
	if mode == SystemShutdown {
		order = []string{ExitStopDispatch, ExitReap, ExitRelease}
	}
	var first error
	for _, name := range order {
		step := s.steps[name]
		sctx, cancel := ctx, context.CancelFunc(func() {})
		if name == ExitCancel {
			sctx, cancel = context.WithTimeout(ctx, s.Grace)
		}
		if s.notify != nil {
			s.notify(Status{Step: name})
		}
		err := step.Run(sctx)
		cancel()
		if err != nil {
			if first == nil {
				first = &StepError{name, err}
			}
			if s.notify != nil {
				s.notify(Status{Step: name, Error: err})
			}
			continue
		}
		if s.notify != nil {
			s.notify(Status{Step: name, Done: true})
		}
	}
	return first
}

// Resettable is what a wake-up has to reset: the scheduler's and the host's heartbeat
// baselines, and a liveness probe of every worker.
type Resettable interface {
	ResetBaseline(now time.Time)
	Probe(ctx context.Context) error
}

// Wake handles the system waking from sleep. Heartbeat baselines are reset first and
// the workers are probed for liveness before anything may judge a timeout, so the time
// the machine slept is not mistaken for a hung worker (architecture 6.2).
func Wake(ctx context.Context, now time.Time, parts ...Resettable) error {
	if len(parts) == 0 {
		return ErrConfig
	}
	for _, p := range parts {
		p.ResetBaseline(now)
	}
	var first error
	for i, p := range parts {
		if err := p.Probe(ctx); err != nil && first == nil {
			first = fmt.Errorf("wake probe %d: %w", i, err)
		}
	}
	return first
}

// Awake declares user-initiated activity to the platform while tasks run and revokes
// it when none do, so the machine is not put to sleep under a running batch.
type Awake struct {
	mu      sync.Mutex
	running int
	declare func(on bool) error
	on      bool
}

func NewAwake(declare func(on bool) error) (*Awake, error) {
	if declare == nil {
		return nil, ErrConfig
	}
	return &Awake{declare: declare}, nil
}

// Set tells how many tasks run now. The platform is called only when the state flips.
func (a *Awake) Set(running int) error {
	if running < 0 {
		return ErrConfig
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = running
	want := running > 0
	if want == a.on {
		return nil
	}
	if err := a.declare(want); err != nil {
		return err
	}
	a.on = want
	return nil
}

// Closing is what happens when the user closes the window or asks to quit.
type Closing int

const (
	Exit        Closing = iota // nothing to protect: leave through the exit order
	HideToTray                 // tasks are running: keep working in the tray
	ConfirmQuit                // an explicit quit while tasks run: ask first
)

// DecideClose is the tray rule: closing the window with tasks running hides it;
// only an explicit quit ends them, and it asks first.
func DecideClose(running int, explicitQuit bool) Closing {
	switch {
	case running <= 0:
		return Exit
	case explicitQuit:
		return ConfirmQuit
	}
	return HideToTray
}

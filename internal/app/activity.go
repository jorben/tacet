package app

import (
	"context"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/platform"
	"sync"
	"time"

	"github.com/jorben/tacet/internal/lifecycle"
)

// activityLinger is how long the declaration outlives the last running stage: a file goes from one stage
// to the next through a moment with none running, and the declaration is not flipped for that.
const activityLinger = 2 * time.Second

// activity tells the platform that work the person started is running while any stage runs, and stops
// telling it once none has run for activityLinger (architecture 6.2, background throttling). Failures of
// the platform are not failures of the work: the stages run either way, at worst throttled.
type activity struct {
	mu      sync.Mutex
	running int
	awake   *lifecycle.Awake
	timer   *time.Timer
	linger  time.Duration
}

func newActivity(declare func(bool) error, linger time.Duration) *activity {
	a, err := lifecycle.NewAwake(declare)
	if err != nil {
		return nil
	}
	return &activity{awake: a, linger: linger}
}

func (a *activity) begin() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running++
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.awake.Set(a.running)
}

func (a *activity) end() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running > 0 {
		a.running--
	}
	if a.running == 0 && a.timer == nil {
		a.timer = time.AfterFunc(a.linger, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.timer = nil
			if a.running == 0 {
				a.awake.Set(0)
			}
		})
	}
}

// stop revokes the declaration at once (the exit order).
func (a *activity) stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.running = 0
	a.awake.Set(0)
}

// Woke is Waker: the hosts' deadlines are rebased before anything may judge one (lifecycle.Wake).
func (e *Engine) Woke(ctx context.Context) error {
	return lifecycle.Wake(ctx, time.Now(), host.Watchdogs{})
}

// OnSystemEnding calls fn when the platform announces a logout, restart or shutdown (macOS; on Windows the
// desktop entry watches the window messages itself).
func OnSystemEnding(fn func()) { platform.OnSystemEnding(fn) }

package host

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrStalled: the plugin did not end its call within the deadline it was given (deadline_ms) and a grace.
// The SDK keeps that deadline itself at each check; a plugin that does not (stuck in native code, in a
// loop without checks) is stopped here, so a hung worker cannot hold a stage for ever.
var ErrStalled = errors.New("PLUGIN_STALLED")

var (
	// stallGrace is the time past deadline_ms the plugin has to report that it stopped.
	stallGrace = 30 * time.Second
	// watchTick is how often the time a call has used is advanced.
	watchTick = time.Second
	// maxStep bounds what one tick may count. A longer gap between ticks is the machine sleeping (or this
	// process not being scheduled), not the plugin working: deadlines count time awake only (architecture
	// 6.2), whatever the platform's clock does during sleep.
	maxStep = 5 * time.Second
)

// watch measures the time one call has used.
type watch struct {
	mu    sync.Mutex
	used  time.Duration
	limit time.Duration
	last  time.Time
}

// advance counts the time since the previous reading, at most maxStep, and reports whether the limit is
// reached.
func (w *watch) advance(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	step := now.Sub(w.last)
	if step < 0 {
		step = 0
	}
	if step > maxStep {
		step = maxStep
	}
	w.used += step
	w.last = now
	return w.used >= w.limit
}

// rebase makes now the reading the next step is counted from: a wake-up resets the baseline, so the time
// asleep is not counted even when a tick came before the notification.
func (w *watch) rebase(now time.Time) {
	w.mu.Lock()
	w.last = now
	w.mu.Unlock()
}

var watches = struct {
	mu  sync.Mutex
	set map[*watch]bool
}{set: map[*watch]bool{}}

// startWatch runs stall when the call has used limit; the returned function ends the watch.
func startWatch(limit time.Duration, stall func()) func() {
	w := &watch{limit: limit, last: time.Now()}
	watches.mu.Lock()
	watches.set[w] = true
	watches.mu.Unlock()
	stop := make(chan struct{})
	tick := watchTick // read here, not in the goroutine: the tick of a call is fixed when it starts
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				if w.advance(now) {
					stall()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			watches.mu.Lock()
			delete(watches.set, w)
			watches.mu.Unlock()
		})
	}
}

// Watchdogs is the hosts' part of a wake-up (lifecycle.Resettable).
type Watchdogs struct{}

// ResetBaseline makes now the baseline of every running call's deadline.
func (Watchdogs) ResetBaseline(now time.Time) {
	watches.mu.Lock()
	defer watches.mu.Unlock()
	for w := range watches.set {
		w.rebase(now)
	}
}

// Probe has nothing to ask: a worker that died while the machine slept ends its stream at once (its pipe
// closes), and one that hangs is caught by its deadline.
func (Watchdogs) Probe(context.Context) error { return nil }

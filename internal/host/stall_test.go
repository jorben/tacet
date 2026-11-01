package host

import (
	"testing"
	"time"
)

// Only time awake counts: a gap between two readings longer than maxStep (the machine slept) counts as
// maxStep, and after a wake-up rebase the gap does not count at all.
func TestADeadlineCountsTimeAwakeOnly(t *testing.T) {
	t0 := time.Now()
	w := &watch{limit: 20 * time.Second, last: t0}
	if w.advance(t0.Add(time.Second)) || w.used != time.Second {
		t.Fatalf("%v", w.used)
	}
	if w.advance(t0.Add(3*time.Hour)) || w.used != time.Second+maxStep { // three hours asleep
		t.Fatalf("a sleep counted as %v", w.used)
	}
	(Watchdogs{}).ResetBaseline(t0) // not registered: no effect, no failure
	w.rebase(t0.Add(5 * time.Hour)) // woken
	if w.advance(t0.Add(5*time.Hour+time.Second)) || w.used != 2*time.Second+maxStep {
		t.Fatalf("after the wake-up only the second since counts: %v", w.used)
	}
	if w.advance(t0.Add(5*time.Hour-time.Second)) || w.used != 2*time.Second+maxStep { // a clock going back counts nothing
		t.Fatalf("a clock going back changed the time used: %v", w.used)
	}
	for i := 2; !w.advance(t0.Add(5*time.Hour + time.Duration(i)*time.Second)); i++ {
		if i > 30 {
			t.Fatal("the limit is never reached")
		}
	}
	if w.used < w.limit {
		t.Fatal()
	}
}

// A wake-up rebases every running watch, so the time between the last tick and the notification is not
// counted against any call.
func TestAWakeUpRebasesEveryRunningCall(t *testing.T) {
	oldTick := watchTick
	watchTick = time.Hour // no tick of its own during the test
	defer func() { watchTick = oldTick }()
	stalled := make(chan struct{}, 1)
	stop := startWatch(time.Minute, func() { stalled <- struct{}{} })
	defer stop()
	watches.mu.Lock()
	var w *watch
	for x := range watches.set {
		w = x
	}
	watches.mu.Unlock()
	if w == nil {
		t.Fatal("the call is not registered")
	}
	later := time.Now().Add(10 * time.Second)
	(Watchdogs{}).ResetBaseline(later)
	if w.advance(later.Add(time.Second)); w.used != time.Second {
		t.Fatalf("%v", w.used)
	}
	stop()
	watches.mu.Lock()
	n := len(watches.set)
	watches.mu.Unlock()
	if n != 0 {
		t.Fatal("an ended call stays registered")
	}
}

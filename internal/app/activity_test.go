package app

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

type declared struct {
	mu    sync.Mutex
	calls []bool
}

func (d *declared) declare(on bool) error {
	d.mu.Lock()
	d.calls = append(d.calls, on)
	d.mu.Unlock()
	return nil
}
func (d *declared) seen() []bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]bool(nil), d.calls...)
}

// A stage that starts within the linger keeps the declaration; the linger counts again from the end of
// the last stage, not from an earlier gap.
func TestActivityLingerCountsFromTheLastStage(t *testing.T) {
	const linger = 200 * time.Millisecond
	d := &declared{}
	a := newActivity(d.declare, linger)
	a.begin()
	a.end() // a gap between two stages
	time.Sleep(linger / 2)
	a.begin()
	time.Sleep(linger / 10)
	a.end()
	time.Sleep(linger * 7 / 10) // past the end of the first gap's linger, within the second's
	if got := d.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("revoked too early, from the first gap: %v", got)
	}
	time.Sleep(linger)
	if got := d.seen(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("%v", got)
	}
}

// The linger fires while a stage is starting (the timer could not be stopped any more): it must not
// revoke the declaration of the work that now runs.
func TestActivityLingerThatFiresAsAStageStartsKeepsTheDeclaration(t *testing.T) {
	const linger = 20 * time.Millisecond
	d := &declared{}
	a := newActivity(d.declare, linger)
	a.begin()
	a.end()
	a.mu.Lock()
	time.Sleep(5 * linger) // the timer has fired and waits for the lock
	a.running = 1          // a stage started meanwhile, as begin would record it
	a.timer = nil
	a.mu.Unlock()
	time.Sleep(5 * linger)
	if got := d.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("revoked under running work: %v", got)
	}
}

package netgate

import (
	"context"
	"errors"
	"sync"
)

var ErrLeaseKind = errors.New("ACTIVITY_KIND_INVALID")
var ErrLeaseReleased = errors.New("ACTIVITY_LEASE_RELEASED")

// BusyError contains only the activity class, never a path or task detail.
type BusyError struct{ Held LeaseKind }

func (e *BusyError) Error() string { return CodeActivityBusy }

// Gate is the single application activity coordinator (MD-19). Processing
// excludes both network and maintenance. Multiple non-conflicting holders are
// reference-counted, so releasing one task cannot unblock a live sibling.
// Activity acquisition never queues or starts work implicitly.
type Gate struct {
	mu   sync.Mutex
	held map[LeaseKind]uint64
}

type Lease struct {
	gate     *Gate
	kind     LeaseKind
	released bool // protected by gate.mu
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewGate() *Gate { return &Gate{held: make(map[LeaseKind]uint64)} }

func validKind(kind LeaseKind) bool {
	return kind == Processing || kind == Network || kind == Maintenance
}

func (g *Gate) Acquire(kind LeaseKind) (*Lease, error) {
	if !validKind(kind) {
		return nil, ErrLeaseKind
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held == nil {
		g.held = make(map[LeaseKind]uint64)
	}
	if kind == Processing {
		for _, other := range []LeaseKind{Network, Maintenance} {
			if g.held[other] > 0 {
				return nil, &BusyError{Held: other}
			}
		}
	} else if g.held[Processing] > 0 {
		return nil, &BusyError{Held: Processing}
	}
	g.held[kind]++
	ctx, cancel := context.WithCancel(context.Background())
	return &Lease{gate: g, kind: kind, ctx: ctx, cancel: cancel}, nil
}

func (l *Lease) Release() error {
	if l == nil || l.gate == nil {
		return ErrLeaseReleased
	}
	l.gate.mu.Lock()
	defer l.gate.mu.Unlock()
	if l.released {
		return ErrLeaseReleased
	}
	l.released = true
	l.gate.held[l.kind]--
	l.cancel() // in-flight work bound to the lease stops with it
	return nil
}

// WhileHeld executes only a short admission callback, ordered against Release.
// It must not perform network I/O or call Acquire/Release on the same gate.
// The outbound client uses it to admit connection attempts before dial; active
// connections must hold their lease until shutdown, not only until admission.
func (l *Lease) WhileHeld(admit func() error) error {
	if l == nil || l.gate == nil {
		return ErrLeaseReleased
	}
	l.gate.mu.Lock()
	defer l.gate.mu.Unlock()
	if l.released {
		return ErrLeaseReleased
	}
	return admit()
}

func (g *Gate) State() map[LeaseKind]uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	copy := make(map[LeaseKind]uint64, len(g.held))
	for kind, count := range g.held {
		if count > 0 {
			copy[kind] = count
		}
	}
	return copy
}

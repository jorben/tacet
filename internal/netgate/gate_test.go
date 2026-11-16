package netgate

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessingExcludesNetworkAndMaintenanceBothDirections(t *testing.T) {
	for _, other := range []LeaseKind{Network, Maintenance} {
		for _, reverse := range []bool{false, true} {
			g := NewGate()
			first, second := Processing, other
			if reverse {
				first, second = second, first
			}
			lease, err := g.Acquire(first)
			if err != nil {
				t.Fatal(err)
			}
			_, err = g.Acquire(second)
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Held != first || err.Error() != CodeActivityBusy {
				t.Fatalf("conflict %s/%s: %v", first, second, err)
			}
			if err = lease.Release(); err != nil {
				t.Fatal(err)
			}
			next, err := g.Acquire(second)
			if err != nil {
				t.Fatal(err)
			}
			next.Release()
		}
	}
}

// A listener is told about a release when acting on it already works: it can take the conflicting lease at once, and
// (being outside the gate's lock) it can call the gate at all. Only a real release counts.
func TestAReleaseListenerFindsTheLeaseGone(t *testing.T) {
	g := NewGate()
	var heard []error
	g.OnRelease(func() {
		_, err := g.Acquire(Processing)
		heard = append(heard, err)
	})
	lease, err := g.Acquire(Network)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() { released <- lease.Release() }()
	select {
	case err := <-released:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the release did not return: the listener ran under the gate's lock")
	}
	if len(heard) != 1 || heard[0] != nil {
		t.Fatalf("what the listener found on acquiring: %v", heard)
	}
	if err := lease.Release(); !errors.Is(err, ErrLeaseReleased) || len(heard) != 1 {
		t.Fatalf("a second release of one lease: %v, heard %d", err, len(heard))
	}
}

func TestMultipleProcessingHoldersKeepExclusionUntilLastRelease(t *testing.T) {
	g := NewGate()
	first, _ := g.Acquire(Processing)
	second, _ := g.Acquire(Processing)
	first.Release()
	if _, err := g.Acquire(Network); err == nil {
		t.Fatal("sibling processing lease lost")
	}
	if err := first.Release(); !errors.Is(err, ErrLeaseReleased) {
		t.Fatal("double release accepted")
	}
	second.Release()
	network, err := g.Acquire(Network)
	if err != nil {
		t.Fatal(err)
	}
	network.Release()
	if len(g.State()) != 0 {
		t.Fatal("lease leak")
	}
}

func TestParallelNonconflictingAndInvalidKinds(t *testing.T) {
	g := NewGate()
	network, _ := g.Acquire(Network)
	maintenance, err := g.Acquire(Maintenance)
	if err != nil {
		t.Fatal(err)
	}
	state := g.State()
	state[Processing] = 99
	if g.State()[Processing] != 0 {
		t.Fatal("state alias")
	}
	network.Release()
	maintenance.Release()
	if _, err := g.Acquire("unknown"); !errors.Is(err, ErrLeaseKind) {
		t.Fatal("unknown activity accepted")
	}
	called := false
	if err := network.WhileHeld(func() error { called = true; return nil }); !errors.Is(err, ErrLeaseReleased) || called {
		t.Fatal("expired lease admitted")
	}
}

func TestConcurrentAcquireNeverMixesProcessingAndNetwork(t *testing.T) {
	g := NewGate()
	var wg sync.WaitGroup
	var violation atomic.Bool
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			kind := Processing
			if index%2 == 0 {
				kind = Network
			}
			for j := 0; j < 200; j++ {
				lease, err := g.Acquire(kind)
				if err != nil {
					continue
				}
				state := g.State()
				if state[Processing] > 0 && (state[Network] > 0 || state[Maintenance] > 0) {
					violation.Store(true)
				}
				lease.Release()
			}
		}(i)
	}
	wg.Wait()
	if violation.Load() {
		t.Fatal("conflicting activities overlapped")
	}
	if len(g.State()) != 0 {
		t.Fatal("lease leaked")
	}
}

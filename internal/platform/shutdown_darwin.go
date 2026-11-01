//go:build darwin

package platform

/*
#cgo LDFLAGS: -framework AppKit
void tacet_observe_power_off(void);
void tacet_post_power_off(void);
*/
import "C"

import "sync"

var ending struct {
	mu sync.Mutex
	fn []func()
}

// OnSystemEnding calls fn when the system is about to log out, restart or shut down (architecture 6.2:
// the time left is short). It needs the application's run loop, which the desktop application has.
func OnSystemEnding(fn func()) {
	ending.mu.Lock()
	ending.fn = append(ending.fn, fn)
	ending.mu.Unlock()
	C.tacet_observe_power_off()
}

//export tacetSystemEnding
func tacetSystemEnding() {
	ending.mu.Lock()
	fns := append([]func(){}, ending.fn...)
	ending.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func postSystemEndingForTest() { C.tacet_post_power_off() }

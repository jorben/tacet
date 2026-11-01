//go:build darwin

package platform

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

// User-initiated activity that still lets the system sleep when idle (architecture 6.2): it keeps App Nap
// from slowing a batch down, never the machine from sleeping.
static void *tacet_activity_begin(void) {
	id token = [[NSProcessInfo processInfo] beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep
		reason:@"Tacet is processing documents"];
	return (void *)[token retain];
}

static void tacet_activity_end(void *token) {
	[[NSProcessInfo processInfo] endActivity:(id)token];
	[(id)token release];
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

var activity struct {
	mu    sync.Mutex
	token unsafe.Pointer
}

// DeclareActivity declares (on) or revokes (off) that the process is doing work the user started. Repeating
// the current state does nothing.
func DeclareActivity(on bool) error {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	switch {
	case on && activity.token == nil:
		activity.token = C.tacet_activity_begin()
		if activity.token == nil {
			return ErrActivityUnavailable
		}
	case !on && activity.token != nil:
		C.tacet_activity_end(activity.token)
		activity.token = nil
	}
	return nil
}

func activityDeclared() bool {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	return activity.token != nil
}

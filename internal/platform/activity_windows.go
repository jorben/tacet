//go:build windows

package platform

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PROCESS_POWER_THROTTLING_STATE of processthreadsapi.h.
type powerThrottling struct {
	Version, ControlMask, StateMask uint32
}

const (
	processPowerThrottling        = 4   // PROCESS_INFORMATION_CLASS ProcessPowerThrottling
	powerThrottlingVersion        = 1   // PROCESS_POWER_THROTTLING_CURRENT_VERSION
	powerThrottlingExecutionSpeed = 0x1 // PROCESS_POWER_THROTTLING_EXECUTION_SPEED (EcoQoS)
)

var (
	procSetProcessInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessInformation")
	activity                  struct {
		mu sync.Mutex
		on bool
	}
)

// DeclareActivity turns power throttling (EcoQoS) off for this process while work runs (on) and gives the
// decision back to the system afterwards (off), architecture 6.2. It never keeps the machine awake.
// The plugin processes are not covered: their throttling is left to the system.
func DeclareActivity(on bool) error {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if on == activity.on {
		return nil
	}
	st := powerThrottling{Version: powerThrottlingVersion}
	if on {
		st.ControlMask = powerThrottlingExecutionSpeed // controlled, and not set: never throttled
	}
	if err := procSetProcessInformation.Find(); err != nil {
		return ErrActivityUnavailable // before Windows 8
	}
	r, _, _ := procSetProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling, uintptr(unsafe.Pointer(&st)), unsafe.Sizeof(st))
	if r == 0 {
		return ErrActivityUnavailable
	}
	activity.on = on
	return nil
}

func activityDeclared() bool {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	return activity.on
}

package gui

import (
	"sync"
	"time"
)

// TrayAnimation alternates two icon frames while work is running. Dispatch must
// enqueue on the UI thread; show is called there, never from the timer goroutine.
// A generation check discards queued frames after a stop, restart or shutdown.
type TrayAnimation struct {
	mu         sync.Mutex
	dispatch   func(func())
	show       func(int)
	timer      *time.Timer
	generation uint64
	running    bool
	closed     bool
}

const trayFrameInterval = 600 * time.Millisecond

func NewTrayAnimation(dispatch func(func()), show func(int)) *TrayAnimation {
	return &TrayAnimation{dispatch: dispatch, show: show}
}

func (a *TrayAnimation) SetRunning(running bool) {
	a.mu.Lock()
	if a.closed || a.running == running {
		a.mu.Unlock()
		return
	}
	a.running = running
	a.generation++
	generation := a.generation
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	a.mu.Unlock()
	// Start and stop both restore frame zero; repeated status updates do not
	// reset the animation. Schedule only after a frame reaches the UI, so a
	// blocked UI cannot accumulate timer callbacks.
	a.dispatch(func() { a.draw(generation, 0) })
}

func (a *TrayAnimation) draw(generation uint64, frame int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || generation != a.generation {
		return
	}
	a.show(frame)
	if a.running {
		a.timer = time.AfterFunc(trayFrameInterval, func() {
			a.dispatch(func() { a.draw(generation, 1-frame) })
		})
	}
}

// Close stops timers and invalidates UI callbacks before the tray is destroyed.
func (a *TrayAnimation) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.generation++
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

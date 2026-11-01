package gui

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestTrayAnimationAlternatesAndStopsWithoutRestartingOnProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := make(chan func(), 8)
		var frames []int
		a := NewTrayAnimation(func(f func()) { ui <- f }, func(frame int) { frames = append(frames, frame) })
		defer a.Close()
		a.SetRunning(false)
		if len(ui) != 0 {
			t.Fatal("idle animation queued work")
		}
		a.SetRunning(true)
		(<-ui)()
		time.Sleep(trayFrameInterval / 2)
		a.SetRunning(true) // progress notifications must not reset the timer
		time.Sleep(trayFrameInterval / 2)
		synctest.Wait()
		(<-ui)()
		time.Sleep(trayFrameInterval)
		synctest.Wait()
		(<-ui)()
		if !reflect.DeepEqual(frames, []int{0, 1, 0}) {
			t.Fatalf("frames: %v", frames)
		}
		a.SetRunning(false)
		(<-ui)()
		time.Sleep(3 * trayFrameInterval)
		synctest.Wait()
		if len(ui) != 0 {
			t.Fatal("idle timer still running")
		}
		if frames[len(frames)-1] != 0 {
			t.Fatal("did not restore idle frame")
		}
	})
}

func TestTrayAnimationDiscardsPendingFramesOnStopRestartAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ui := make(chan func(), 8)
		var frames []int
		a := NewTrayAnimation(func(f func()) { ui <- f }, func(frame int) { frames = append(frames, frame) })
		a.SetRunning(true)
		(<-ui)()
		time.Sleep(trayFrameInterval)
		synctest.Wait()
		stale := <-ui
		time.Sleep(10 * trayFrameInterval)
		synctest.Wait()
		if len(ui) != 0 {
			t.Fatal("blocked UI accumulated frames")
		}
		a.SetRunning(false)
		idle := <-ui
		a.SetRunning(true)
		stale()
		idle() // neither generation may change the restarted animation
		if !reflect.DeepEqual(frames, []int{0}) {
			t.Fatalf("stale frames displayed: %v", frames)
		}
		(<-ui)()
		time.Sleep(trayFrameInterval)
		synctest.Wait()
		pending := <-ui
		a.Close()
		pending()
		a.SetRunning(true)
		time.Sleep(3 * trayFrameInterval)
		synctest.Wait()
		if len(ui) != 0 || !reflect.DeepEqual(frames, []int{0, 0}) {
			t.Fatalf("updated after close: %v", frames)
		}
	})
}

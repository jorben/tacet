package gui

import (
	"context"
	"fmt"
	"time"

	"github.com/jorben/tacet/internal/app"
)

// EventQuitRequested is emitted to the frontend when the person chooses "退出" in the tray: the window
// decides as for an explicit quit (requirements 7.1: unfinished work is asked about once, centrally).
const EventQuitRequested = "tacet:quit-requested"

// Activity is what the tray shows (architecture 6.2): files being worked on and files waiting for the
// person, over the unfinished batches. It comes from the same verdicts the window and the CLI show.
type Activity struct {
	Running int
	Waiting int
}

func activityOf(bs []app.BatchStatus) Activity {
	var a Activity
	for _, b := range bs {
		a.Running += b.Counts["processing"]
		a.Waiting += b.Counts["awaiting_review"] + b.Counts["awaiting_publish"] + b.Counts["blocked"] + b.Counts["failed"]
	}
	return a
}

// Label is the tray's status line.
func (a Activity) Label() string {
	switch {
	case a.Running == 0 && a.Waiting == 0:
		return "没有进行中的任务"
	case a.Waiting == 0:
		return fmt.Sprintf("正在处理 %d 份", a.Running)
	case a.Running == 0:
		return fmt.Sprintf("%d 份等你处理", a.Waiting)
	}
	return fmt.Sprintf("正在处理 %d 份 · %d 份等你处理", a.Running, a.Waiting)
}

// TrayHost is a Host that also has a tray to show the activity in. Hosts without one (tests of other
// behaviour, the browser) simply do not show it.
type TrayHost interface {
	ShowActivity(Activity)
}

// refreshTray reads the unfinished batches and shows their activity in the tray.
func (s *Service) refreshTray() {
	th, ok := s.host.(TrayHost)
	if !ok {
		return
	}
	s.mu.Lock()
	core := s.core
	s.mu.Unlock()
	if core == nil {
		th.ShowActivity(Activity{})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bs, err := core.Unfinished(ctx)
	if err != nil {
		return // the tray keeps what it showed; the window reports the problem
	}
	th.ShowActivity(activityOf(bs))
}

// Woke is called when the system wakes from sleep (architecture 6.2). The engine rebases the deadlines of
// the running plugin calls first, so the sleep does not read as a hung worker; then what the window and the
// tray show is read again, because events may have been held back while asleep. It is a function, not a
// bound method: the frontend cannot call it.
func Woke(svc *Service) {
	svc.mu.Lock()
	core := svc.core
	svc.mu.Unlock()
	if core == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if w, ok := core.(app.Waker); ok {
		w.Woke(ctx) // first: no deadline is judged on a baseline from before the sleep
	}
	bs, err := core.Unfinished(ctx)
	if err != nil || len(bs) == 0 {
		svc.refreshTray()
		return
	}
	for _, b := range bs {
		svc.changed(b.ID) // re-emitted to the window, and the tray follows
	}
}

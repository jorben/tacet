package gui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/app"
)

type trayCore struct {
	fakeCore
	list []app.BatchStatus
}

func (c *trayCore) Unfinished(context.Context) ([]app.BatchStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]app.BatchStatus(nil), c.list...), nil
}

type trayHost struct {
	fakeHost
	tmu   sync.Mutex
	shown []Activity
}

func (h *trayHost) ShowActivity(a Activity) {
	h.tmu.Lock()
	h.shown = append(h.shown, a)
	h.tmu.Unlock()
}
func (h *trayHost) latest() (Activity, int) {
	h.tmu.Lock()
	defer h.tmu.Unlock()
	if len(h.shown) == 0 {
		return Activity{}, 0
	}
	return h.shown[len(h.shown)-1], len(h.shown)
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("not reached in time")
}

func TestTheTrayLabelSaysWhatRunsAndWhatWaits(t *testing.T) {
	for _, c := range []struct {
		a    Activity
		want string
	}{{Activity{}, "没有进行中的任务"}, {Activity{Running: 3}, "正在处理 3 份"}, {Activity{Waiting: 2}, "2 份等你处理"},
		{Activity{Running: 3, Waiting: 2}, "正在处理 3 份 · 2 份等你处理"}} {
		if got := c.a.Label(); got != c.want {
			t.Errorf("%+v: %q", c.a, got)
		}
	}
	got := activityOf([]app.BatchStatus{
		{Active: 2, Counts: map[string]int{"processing": 2, "awaiting_review": 1, "completed_per_policy": 5}},
		{Active: 1, Counts: map[string]int{"processing": 1, "awaiting_publish": 1, "blocked": 1, "failed": 1, "skipped": 4, "unprocessable": 2}},
		{Counts: map[string]int{"processing": 200}}, // paused/draft files share this verdict but are inactive
	})
	if got != (Activity{Running: 3, Waiting: 4}) {
		t.Fatalf("%+v", got)
	}
}

// The tray follows the same statuses the window gets: at once when the engine opens, and after each change.
func TestTheTrayFollowsTheUnfinishedBatches(t *testing.T) {
	c := &trayCore{list: []app.BatchStatus{{ID: "b0001", Active: 2, Counts: map[string]int{"processing": 2, "awaiting_review": 1}}}}
	h := &trayHost{}
	s := NewService(opener(c, nil), h)
	defer Shutdown(s)
	if _, err := s.engine(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { a, _ := h.latest(); return a == Activity{Running: 2, Waiting: 1} })
	c.mu.Lock()
	c.list = []app.BatchStatus{{ID: "b0001", Counts: map[string]int{"completed_per_policy": 2, "awaiting_review": 1}}}
	c.status = c.list[0]
	c.mu.Unlock()
	c.notify("b0001")
	eventually(t, func() bool { a, _ := h.latest(); return a == Activity{Waiting: 1} })
}

// After a wake-up every unfinished batch is read again and sent to the window, and the tray follows.
func TestAfterAWakeUpTheWindowAndTheTrayAreReadAgain(t *testing.T) {
	Woke(NewService(nil, &trayHost{})) // no engine yet: nothing to read, no failure
	c := &trayCore{list: []app.BatchStatus{{ID: "b0001", Active: 1, Counts: map[string]int{"processing": 1}}}}
	c.status = c.list[0]
	h := &trayHost{}
	s := NewService(opener(c, nil), h)
	defer Shutdown(s)
	s.engine()
	eventually(t, func() bool { _, n := h.latest(); return n >= 1 })
	before := h.count()
	_, shown := h.latest()
	c.mu.Lock()
	c.list[0].Counts = map[string]int{"awaiting_publish": 1}
	c.list[0].Active = 0
	c.mu.Unlock()
	Woke(s)
	eventually(t, func() bool { return h.count() > before })
	eventually(t, func() bool { a, n := h.latest(); return n > shown && a == Activity{Waiting: 1} })
}

type wakeCore struct {
	trayCore
	order []string
}

func (c *wakeCore) Woke(context.Context) error {
	c.mu.Lock()
	c.order = append(c.order, "woke")
	c.mu.Unlock()
	return nil
}
func (c *wakeCore) Unfinished(ctx context.Context) ([]app.BatchStatus, error) {
	c.mu.Lock()
	c.order = append(c.order, "read")
	c.mu.Unlock()
	return c.trayCore.Unfinished(ctx)
}

// The engine rebases its deadlines before the state is read again after a wake-up.
func TestAWakeUpRebasesTheEngineDeadlinesBeforeReading(t *testing.T) {
	c := &wakeCore{trayCore: trayCore{list: []app.BatchStatus{{ID: "b0001", Counts: map[string]int{"processing": 1}}}}}
	s := NewService(opener(c, nil), &fakeHost{})
	defer Shutdown(s)
	s.engine()
	c.mu.Lock()
	c.order = nil
	c.mu.Unlock()
	Woke(s)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.order) < 2 || c.order[0] != "woke" || c.order[1] != "read" {
		t.Fatalf("%v", c.order)
	}
}

type endingCore struct {
	fakeCore
	how string
}

func (c *endingCore) CloseForSystem() error { c.how = "system"; return nil }
func (c *endingCore) Close() error          { c.how = "normal"; return nil }

// After the system announced its end the exit takes the short order; otherwise the normal one.
func TestTheExitTakesTheShortOrderOnlyWhenTheSystemIsEnding(t *testing.T) {
	for _, ending := range []bool{false, true} {
		c := &endingCore{}
		s := NewService(opener(c, nil), &fakeHost{})
		s.engine()
		if ending {
			SystemEnding(s)
		}
		Shutdown(s)
		if want := map[bool]string{false: "normal", true: "system"}[ending]; c.how != want {
			t.Fatalf("ending=%v: %s", ending, c.how)
		}
	}
}

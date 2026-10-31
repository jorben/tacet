package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recorder struct {
	log  []string
	fail map[string]error
	hook map[string]func(context.Context)
}

func (r *recorder) step(name string) Step {
	return Step{Name: name, Run: func(ctx context.Context) error {
		r.log = append(r.log, name)
		if h := r.hook[name]; h != nil {
			h(ctx)
		}
		return r.fail[name]
	}}
}

func (r *recorder) starts() []Step {
	var s []Step
	for _, n := range []string{StepWorkspace, StepReap, StepPublish, StepSweep, StepRecover, StepDispatch} {
		s = append(s, r.step(n))
	}
	return s
}

func (r *recorder) stops() []Step {
	var s []Step
	for _, n := range []string{ExitStopDispatch, ExitCancel, ExitCheckpoint, ExitReap, ExitPublishWait, ExitRetention, ExitRelease} {
		s = append(s, r.step(n))
	}
	return s
}

func joined(r *recorder) string { return strings.Join(r.log, ",") }

func TestStartRunsTheDesignedOrderAndOpensOperationsOnlyAtTheEnd(t *testing.T) {
	r := &recorder{}
	var seen []Status
	opened := []bool{}
	var st *Starter
	st, err := NewStarter(r.starts(), func(s Status) { seen = append(seen, s); opened = append(opened, st.Open()) })
	if err != nil {
		t.Fatal(err)
	}
	if st.Open() {
		t.Fatal("open before start")
	}
	if err := st.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if joined(r) != "workspace,reap,publish,retention_sweep,recover,dispatch" || !st.Open() {
		t.Fatalf("%s", joined(r))
	}
	for i, o := range opened {
		if o {
			t.Fatalf("operations were open during step %d", i)
		}
	}
	if len(seen) != 12 || seen[0].Step != StepWorkspace || seen[0].Done || !seen[1].Done || !seen[11].Done {
		t.Fatalf("%+v", seen)
	}
	if err := st.Start(context.Background()); !errors.Is(err, ErrRunning) {
		t.Fatalf("%v", err)
	}
}

func TestAFailedStartStepStopsTheOrderAndLeavesOperationsClosed(t *testing.T) {
	for _, failing := range []string{StepWorkspace, StepReap, StepPublish, StepSweep, StepRecover, StepDispatch} {
		boom := errors.New("boom")
		r := &recorder{fail: map[string]error{failing: boom}}
		var last Status
		st, _ := NewStarter(r.starts(), func(s Status) { last = s })
		err := st.Start(context.Background())
		var se *StepError
		if !errors.As(err, &se) || se.Step != failing || !errors.Is(err, boom) {
			t.Fatalf("%s: %v", failing, err)
		}
		if st.Open() {
			t.Fatalf("%s: operations opened on a workspace that was not recovered", failing)
		}
		if !strings.HasSuffix(joined(r), failing) {
			t.Fatalf("%s: later steps ran: %s", failing, joined(r))
		}
		if last.Step != failing || last.Error == nil {
			t.Fatalf("%s: %+v", failing, last)
		}
	}
}

func TestACancelledStartRunsNothingFurther(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	r.hook = map[string]func(context.Context){StepReap: func(context.Context) { cancel() }}
	st, _ := NewStarter(r.starts(), nil)
	if err := st.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if joined(r) != "workspace,reap" || st.Open() {
		t.Fatalf("%s", joined(r))
	}
}

func TestTheOrdersMustBeExactlyTheDesignedOnes(t *testing.T) {
	r := &recorder{}
	good := r.starts()
	swapped := append([]Step(nil), good...)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	missing := good[:5]
	empty := append([]Step(nil), good...)
	empty[3].Run = nil
	for name, s := range map[string][]Step{"swapped": swapped, "missing": missing, "no action": empty, "none": nil} {
		if _, err := NewStarter(s, nil); !errors.Is(err, ErrConfig) {
			t.Errorf("start %s: %v", name, err)
		}
	}
	stops := r.stops()
	sw := append([]Step(nil), stops...)
	sw[0], sw[6] = sw[6], sw[0]
	for name, s := range map[string][]Step{"swapped": sw, "missing": stops[:6], "none": nil} {
		if _, err := NewStopper(s, time.Second, nil); !errors.Is(err, ErrConfig) {
			t.Errorf("stop %s: %v", name, err)
		}
	}
	if _, err := NewStopper(stops, 0, nil); !errors.Is(err, ErrConfig) {
		t.Error("no grace period")
	}
}

func TestANormalExitRunsEveryStepInOrderAndReleasesTheWorkspaceLast(t *testing.T) {
	r := &recorder{}
	sp, _ := NewStopper(r.stops(), time.Second, nil)
	if err := sp.Stop(context.Background(), Normal); err != nil {
		t.Fatal(err)
	}
	if joined(r) != "stop_dispatch,cancel_to_boundary,checkpoint,reap,publish_wait,retention,release_workspace" {
		t.Fatalf("%s", joined(r))
	}
	n := len(r.log)
	if err := sp.Stop(context.Background(), Normal); err != nil || len(r.log) != n {
		t.Fatalf("a second exit must do nothing: %v", err)
	}
}

func TestAFailingExitStepDoesNotPreventTheRestAndTheFirstFailureIsReported(t *testing.T) {
	r := &recorder{fail: map[string]error{ExitCheckpoint: errors.New("disk"), ExitRetention: errors.New("later")}}
	var errs []string
	sp, _ := NewStopper(r.stops(), time.Second, func(s Status) {
		if s.Error != nil {
			errs = append(errs, s.Step)
		}
	})
	err := sp.Stop(context.Background(), Normal)
	var se *StepError
	if !errors.As(err, &se) || se.Step != ExitCheckpoint {
		t.Fatalf("%v", err)
	}
	if !strings.HasSuffix(joined(r), "release_workspace") || len(r.log) != 7 {
		t.Fatalf("an exit must go as far as it can: %s", joined(r))
	}
	if strings.Join(errs, ",") != "checkpoint,retention" {
		t.Fatalf("%v", errs)
	}
}

func TestTheCancellationStepIsBoundedByTheGraceAndTheOthersAreNot(t *testing.T) {
	r := &recorder{hook: map[string]func(context.Context){}}
	got := map[string]time.Duration{}
	for _, n := range []string{ExitCancel, ExitReap, ExitPublishWait} {
		n := n
		r.hook[n] = func(ctx context.Context) {
			if d, ok := ctx.Deadline(); ok {
				got[n] = time.Until(d)
			}
		}
	}
	sp, _ := NewStopper(r.stops(), 400*time.Millisecond, nil)
	sp.Stop(context.Background(), Normal)
	if d, ok := got[ExitCancel]; !ok || d <= 0 || d > 400*time.Millisecond {
		t.Fatalf("%v", got)
	}
	if _, ok := got[ExitReap]; ok {
		t.Fatal("reaping must not inherit the grace deadline: it is what cleans up when the grace ran out")
	}
	if _, ok := got[ExitPublishWait]; ok {
		t.Fatal("a publish commit in progress is waited for, not cut off")
	}
}

func TestACancellationThatRunsOutOfGraceStillReapsAndReleases(t *testing.T) {
	r := &recorder{hook: map[string]func(context.Context){ExitCancel: func(ctx context.Context) { <-ctx.Done() }}, fail: map[string]error{}}
	r.fail[ExitCancel] = context.DeadlineExceeded
	sp, _ := NewStopper(r.stops(), 50*time.Millisecond, nil)
	start := time.Now()
	err := sp.Stop(context.Background(), Normal)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("%v %v", err, time.Since(start))
	}
	if !strings.Contains(joined(r), "reap") || !strings.HasSuffix(joined(r), "release_workspace") {
		t.Fatalf("%s", joined(r))
	}
}

func TestSystemShutdownSkipsWaitingAndCleaningButStillReapsAndReleases(t *testing.T) {
	r := &recorder{}
	sp, _ := NewStopper(r.stops(), time.Second, nil)
	if err := sp.Stop(context.Background(), SystemShutdown); err != nil {
		t.Fatal(err)
	}
	if joined(r) != "stop_dispatch,reap,release_workspace" {
		t.Fatalf("%s", joined(r))
	}
}

type part struct {
	name  string
	log   *[]string
	probe error
}

func (p part) ResetBaseline(time.Time)     { *p.log = append(*p.log, "reset "+p.name) }
func (p part) Probe(context.Context) error { *p.log = append(*p.log, "probe "+p.name); return p.probe }

func TestWakeResetsEveryBaselineBeforeAnyProbeSoSleepIsNotATimeout(t *testing.T) {
	var log []string
	err := Wake(context.Background(), time.Now(), part{"sched", &log, nil}, part{"host", &log, errors.New("dead")}, part{"x", &log, errors.New("also")})
	if err == nil || !strings.Contains(err.Error(), "wake probe 1") {
		t.Fatalf("the first failure is reported: %v", err)
	}
	want := "reset sched,reset host,reset x,probe sched,probe host,probe x"
	if strings.Join(log, ",") != want {
		t.Fatalf("%v", log)
	}
	if err := Wake(context.Background(), time.Now()); !errors.Is(err, ErrConfig) {
		t.Fatalf("%v", err)
	}
	log = nil
	if err := Wake(context.Background(), time.Now(), part{"a", &log, nil}); err != nil {
		t.Fatal(err)
	}
}

func TestActivityIsDeclaredWhileTasksRunAndRevokedWhenNoneDoAndOnlyOnChange(t *testing.T) {
	var calls []bool
	a, err := NewAwake(func(on bool) error { calls = append(calls, on); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 3, 2, 0, 0, 1} {
		if err := a.Set(n); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls) != 3 || !calls[0] || calls[1] || !calls[2] {
		t.Fatalf("%v", calls)
	}
	if err := a.Set(-1); !errors.Is(err, ErrConfig) {
		t.Fatalf("%v", err)
	}
	if _, err := NewAwake(nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("%v", err)
	}
	boom := errors.New("platform")
	f, _ := NewAwake(func(bool) error { return boom })
	if err := f.Set(1); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	n := 0
	f2, _ := NewAwake(func(bool) error {
		n++
		if n == 1 {
			return boom
		}
		return nil
	})
	f2.Set(1)
	if err := f2.Set(1); err != nil || n != 2 {
		t.Fatalf("a failed declaration must be retried, not assumed: %v %d", err, n)
	}
}

func TestClosingTheWindowWithRunningTasksHidesItAndOnlyAnExplicitQuitAsks(t *testing.T) {
	for _, c := range []struct {
		running int
		quit    bool
		want    Closing
	}{{0, false, Exit}, {0, true, Exit}, {2, false, HideToTray}, {2, true, ConfirmQuit}, {-1, false, Exit}} {
		if got := DecideClose(c.running, c.quit); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/app"
)

// fake is a Core whose answers the test dictates.
type fake struct {
	mu      sync.Mutex
	status  app.BatchStatus
	waitErr error
	// startErr, cancelErr and resumeErr are what Start, Cancel and Resume answer.
	startErr, cancelErr, resumeErr error
	// onStart and onWait run as that call comes in: to look at what the person sees by then, or to interrupt there.
	onStart, onWait func()
	calls           []string
	started         app.StartRequest
	// statusDelay slows Status; waitDelay slows Wait: together they leave a status read in
	// flight when the wait ends.
	statusDelay, waitDelay time.Duration
	// waitBlocks makes Wait end only when its context does.
	waitBlocks bool
}

func (f *fake) rec(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fake) Import(_ context.Context, r app.ImportRequest) (app.BatchStatus, error) {
	f.rec("import")
	return app.BatchStatus{ID: "b0001", Total: len(r.Files)}, nil
}
func (f *fake) Preflight(context.Context, string) (app.BatchStatus, error) {
	f.rec("preflight")
	return f.status, nil
}
func (f *fake) Start(_ context.Context, r app.StartRequest) error {
	f.rec("start")
	f.started = r
	if f.onStart != nil {
		f.onStart()
	}
	return f.startErr
}
func (f *fake) Status(context.Context, string, int) (app.BatchStatus, error) {
	time.Sleep(f.statusDelay)
	return f.status, nil
}
func (f *fake) Wait(ctx context.Context, _ string) (app.BatchStatus, error) {
	f.rec("wait")
	if f.onWait != nil {
		f.onWait()
	}
	if f.waitBlocks {
		<-ctx.Done()
		return app.BatchStatus{}, ctx.Err()
	}
	time.Sleep(f.waitDelay)
	if f.waitErr != nil {
		return app.BatchStatus{}, f.waitErr
	}
	return f.status, nil
}
func (f *fake) DecideFindings(context.Context, string, []app.FindingDecision) error { return nil }
func (f *fake) ConfirmFile(context.Context, string) error                           { return nil }
func (f *fake) Skip(context.Context, string) error                                  { return nil }
func (f *fake) WithdrawManual(context.Context, string, int64) error                 { return nil }
func (f *fake) AddManual(context.Context, string, int, int, int, string) (int64, error) {
	return 0, nil
}
func (f *fake) Files(context.Context, string, int, int) ([]app.FileStatus, error) { return nil, nil }
func (f *fake) PagePreview(context.Context, string, int, int) (app.PagePreview, error) {
	return app.PagePreview{}, nil
}
func (f *fake) Unfinished(context.Context) ([]app.BatchStatus, error)        { return nil, nil }
func (f *fake) ConfigList(context.Context, string) ([]app.ConfigItem, error) { return nil, nil }
func (f *fake) ConfigCreate(context.Context, string, string, string, string) (app.ConfigItem, error) {
	return app.ConfigItem{}, nil
}
func (f *fake) ConfigSave(context.Context, string, uint64, string, string, string) (app.ConfigItem, error) {
	return app.ConfigItem{}, nil
}
func (f *fake) ConfigCopy(context.Context, string, string) (app.ConfigItem, error) {
	return app.ConfigItem{}, nil
}
func (f *fake) ConfigDelete(context.Context, string, uint64) error     { return nil }
func (f *fake) Plugins(context.Context) ([]app.PluginInfo, error)      { return nil, nil }
func (f *fake) Models(context.Context) ([]app.ModelRuntime, error)     { return nil, nil }
func (f *fake) Categories(context.Context) ([]app.CategoryInfo, error) { return nil, nil }
func (f *fake) DownloadModel(context.Context, string) error            { return nil }
func (f *fake) PauseModel(context.Context, string) error               { return nil }
func (f *fake) DiscardModel(context.Context, string) error             { return nil }
func (f *fake) PrepareModel(context.Context, string) error             { return nil }
func (f *fake) ModelDirectory(context.Context, string) (string, error) { return "", nil }
func (f *fake) WorkspaceInfo(context.Context) (app.WorkspaceInfo, error) {
	return app.WorkspaceInfo{}, nil
}
func (f *fake) SettingsGet(context.Context) (app.Settings, error) { return app.Settings{}, nil }
func (f *fake) SettingsSave(context.Context, app.Settings) (app.Settings, error) {
	return app.Settings{}, nil
}
func (f *fake) ConfigHistory(context.Context, string) ([]app.ConfigVersion, error) { return nil, nil }
func (f *fake) ConfigRollback(context.Context, string, uint64, uint64) (app.ConfigItem, error) {
	return app.ConfigItem{}, nil
}
func (f *fake) TestPreset(context.Context, string, string) (app.PresetTest, error) {
	return app.PresetTest{}, nil
}
func (f *fake) Publish(context.Context, string, bool, string) error                 { f.rec("publish"); return nil }
func (f *fake) Unlock(context.Context, string, []byte) error                        { return nil }
func (f *fake) Document(context.Context, string, int, int) ([]app.DocUnit, error)   { return nil, nil }
func (f *fake) Findings(context.Context, string, int64, int) ([]app.Finding, error) { return nil, nil }
func (f *fake) DecideFinding(context.Context, string, int64, bool) error            { return nil }
func (f *fake) Pause(context.Context, string) error                                 { f.rec("pause"); return nil }
func (f *fake) Resume(context.Context, string) error                                { f.rec("resume"); return f.resumeErr }
func (f *fake) Cancel(context.Context, string) error                                { f.rec("cancel"); return f.cancelErr }
func (f *fake) Retry(_ context.Context, _ string, j []string) error {
	f.rec("retry:" + strings.Join(j, ","))
	return nil
}
func (f *fake) Clean(context.Context, string) error { f.rec("clean"); return nil }
func (f *fake) Close() error                        { f.rec("close"); return nil }

func opener(f *fake, err error) Opener {
	return func(context.Context, string) (app.Core, error) {
		if err != nil {
			return nil, err
		}
		return f, nil
	}
}

func call(ctx context.Context, o Opener, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(ctx, args, &out, &errOut, o)
	return code, out.String(), errOut.String()
}

func TestUsageAndExplicitInputs(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{nil, ExitUsage, "Usage"},
		{[]string{"--help"}, ExitOK, "Usage"},
		{[]string{"version"}, ExitOK, "Tacet"},
		{[]string{"nope"}, ExitUsage, "unknown command"},
		{[]string{"run", "--authorize", "--out", dir, "x.txt"}, ExitUsage, "POLICY_REQUIRED"},
		{[]string{"run", "--preset", "p", "--out", dir, "x.txt"}, ExitUsage, "AUTHORIZATION_REQUIRED"},
		{[]string{"run", "--preset", "p", "--authorize", "x.txt"}, ExitUsage, "OUTPUT_REQUIRED"},
		{[]string{"run", "--preset", "p", "--authorize", "--out", dir}, ExitUsage, "NO_FILES"},
		{[]string{"status"}, ExitUsage, "BATCH_REQUIRED"},
		{[]string{"resume"}, ExitUsage, "BATCH_REQUIRED"},
		{[]string{"retry"}, ExitUsage, "BATCH_REQUIRED"},
		{[]string{"clean"}, ExitUsage, "BATCH_REQUIRED"},
		{[]string{"run", "--no-such-flag"}, ExitUsage, "flag provided but not defined"},
	} {
		f := &fake{}
		code, out, errOut := call(context.Background(), opener(f, nil), tc.args...)
		if code != tc.code || !strings.Contains(out+errOut, tc.want) {
			t.Errorf("%v = %d %q %q, want %d %q", tc.args, code, out, errOut, tc.code, tc.want)
		}
		if len(f.calls) > 0 {
			t.Errorf("%v reached the core before the inputs were valid: %v", tc.args, f.calls)
		}
	}
}

func TestExitStatusMirrorsTheCoreVerdict(t *testing.T) {
	for _, code := range []int{ExitOK, ExitIncomplete} {
		f := &fake{status: app.BatchStatus{ID: "b0001", Phase: "x", ExitCode: code, Files: []app.FileStatus{{Name: "f0001", Status: "s", State: "t", Detail: "WHY", Pending: 2}}}}
		got, out, _ := call(context.Background(), opener(f, nil), "status", "--batch", "b0001")
		if got != code || !strings.Contains(out, "reason=WHY") || !strings.Contains(out, "pending=2") {
			t.Errorf("status = %d %q, want %d", got, out, code)
		}
		f.calls = nil
		list := t.TempDir() + "/list.txt"
		writeFile(t, list, "# comment\n"+t.TempDir()+"/a.txt\n\n")
		got, out, _ = call(context.Background(), opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), "--list", list)
		if got != code || strings.Join(f.calls, ",") != "import,start,wait,close" || !f.started.Authorize {
			t.Errorf("run = %d %q calls %v started %+v", got, out, f.calls, f.started)
		}
	}
}

func TestInterruptPausesAndReturnsThree(t *testing.T) {
	f := &fake{waitErr: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	code, _, errOut := call(ctx, opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitInterrupted || !strings.Contains(strings.Join(f.calls, ","), "pause") || !strings.Contains(errOut, "tacet resume --batch b0001") {
		t.Fatalf("code %d calls %v err %q", code, f.calls, errOut)
	}
}

// This run was the only owner of its draft: a start the core refused must not leave it holding the batch
// slot. The order matters: Resume carries out the kept start request, and only files already cancelled
// guarantee that this cannot begin processing after the refusal has been reported.
func TestARefusedStartIsReportedAndReleasesItsDraft(t *testing.T) {
	refused := fmt.Errorf("%w: %w", app.ErrNotReady, app.ErrModelNotInstalled)
	for _, tc := range []struct {
		name  string
		flags []string
		kind  string
	}{{"batch", nil, "tacet: NOT_READY\n"}, {"quick", []string{"--quick"}, "tacet: NOT_READY\n"}, {"json", []string{"--json"}, `"code":"NOT_READY"`}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{startErr: refused}
			args := append([]string{"run", "--preset", "p", "--authorize", "--out", t.TempDir()}, tc.flags...)
			code, _, errOut := call(context.Background(), opener(f, nil), append(args, t.TempDir()+"/a.txt")...)
			if code != ExitUsage || !strings.Contains(errOut, tc.kind) {
				t.Fatalf("code %d err %q", code, errOut)
			}
			if got := strings.Join(f.calls, ","); got != "import,start,cancel,resume,wait,clean,close" {
				t.Fatalf("calls %s", got)
			}
		})
	}
}

// When the files could not be cancelled, Resume could still begin them: nothing more is tried.
func TestARefusedStartWhoseDraftCannotBeCancelledIsNotResumed(t *testing.T) {
	f := &fake{startErr: app.ErrNotReady, cancelErr: errors.New("cancel failed")}
	code, _, errOut := call(context.Background(), opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitUsage || !strings.Contains(errOut, "tacet: NOT_READY") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "import,start,cancel,close" {
		t.Fatalf("calls %s", got)
	}
}

// A Resume that fails has not dropped the kept start, so a wait for it would only end on an interrupt: the
// run skips the wait, and still reports its refusal and cleans up.
func TestARefusedStartWhoseResumeFailsIsNotWaitedFor(t *testing.T) {
	f := &fake{startErr: app.ErrNotReady, resumeErr: errors.New("resume failed")}
	code, _, errOut := call(context.Background(), opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitUsage || !strings.Contains(errOut, "tacet: NOT_READY") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "import,start,cancel,resume,clean,close" {
		t.Fatalf("calls %s", got)
	}
}

// Discarding the draft can wait: the person already has the refusal while it does, and gets it only once.
func TestARefusalIsReportedBeforeItsDraftIsDiscarded(t *testing.T) {
	var out, errOut bytes.Buffer
	var whileWaiting string
	f := &fake{startErr: app.ErrNotReady, onWait: func() { whileWaiting = errOut.String() }}
	code := Run(context.Background(), []string{"run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir() + "/a.txt"}, &out, &errOut, opener(f, nil))
	if code != ExitUsage || whileWaiting != "tacet: NOT_READY\n" || errOut.String() != "tacet: NOT_READY\n" {
		t.Fatalf("code %d, while waiting %q, at the end %q", code, whileWaiting, errOut.String())
	}
}

// Discarding the draft waits for the kept start to be dropped; an interrupt ends that wait, and the run
// still reports its refusal and cleans up. The interrupt comes during the wait: one that came before the
// answer of Start would be an interrupted start, not a refused one.
func TestAnInterruptEndsTheWaitOfARefusedStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{startErr: app.ErrNotReady, waitBlocks: true, onWait: cancel}
	code, _, errOut := call(ctx, opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitUsage || !strings.Contains(errOut, "tacet: NOT_READY") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "import,start,cancel,resume,wait,clean,close" {
		t.Fatalf("calls %s", got)
	}
}

// An interrupt while the start waits for the inspection is not a refusal: the draft stays, paused, for resume.
func TestAnInterruptDuringStartPausesAndKeepsTheDraft(t *testing.T) {
	f := &fake{startErr: context.Canceled}
	code, _, errOut := call(context.Background(), opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitInterrupted || !strings.Contains(errOut, "tacet resume --batch b0001") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "import,start,pause,close" {
		t.Fatalf("calls %s", got)
	}
}

// An interrupt that cuts off a read inside Start's transaction comes back as what that read was for, not as
// context.Canceled. It is still an interrupt: the draft stays, paused, and the person is told how to resume.
func TestAnInterruptInsideStartIsNotTakenForARefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{startErr: app.ErrNotFound, onStart: cancel}
	code, _, errOut := call(ctx, opener(f, nil), "run", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	if code != ExitInterrupted || !strings.Contains(errOut, "tacet resume --batch b0001") || strings.Contains(errOut, "NOT_FOUND") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "import,start,pause,close" {
		t.Fatalf("calls %s", got)
	}
}

func TestOpenFailuresAreCodedAndExitOne(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{{errors.New("WORKSPACE_BUSY"), "WORKSPACE_BUSY"}, {errors.New("KEYSTORE_UNAVAILABLE"), "KEYSTORE_UNAVAILABLE"}, {errors.New("open /Users/secret/x: boom"), "INTERNAL_ERROR"}} {
		code, _, errOut := call(context.Background(), opener(nil, tc.err), "status", "--batch", "b1")
		if code != ExitUsage || !strings.Contains(errOut, tc.want) || strings.Contains(errOut, "secret") {
			t.Errorf("%v: %d %q", tc.err, code, errOut)
		}
	}
}

func TestJSONOutputIsOneObjectPerLine(t *testing.T) {
	f := &fake{status: app.BatchStatus{ID: "b0001", Phase: "all_delivered", Delivered: 1, Total: 1, Counts: map[string]int{"completed_per_policy": 1}}}
	code, out, _ := call(context.Background(), opener(f, nil), "run", "--json", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir()+"/a.txt")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) < 2 || !strings.HasPrefix(lines[0], `{"data":{"batch":"b0001"`) || !strings.Contains(lines[len(lines)-1], `"event":"result"`) {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRetryAndCleanPassScope(t *testing.T) {
	f := &fake{status: app.BatchStatus{ID: "b0001"}}
	call(context.Background(), opener(f, nil), "retry", "--batch", "b0001", "--file", "j1", "--file", "j2")
	call(context.Background(), opener(f, nil), "clean", "--batch", "b0001")
	if got := strings.Join(f.calls, ","); !strings.Contains(got, "retry:j1,j2") || !strings.Contains(got, "clean") {
		t.Fatal(got)
	}
}

// The progress reporter must have finished writing before the result is printed. Run with -race:
// a reporter that outlives follow writes into the buffer while the result is being printed.
func TestProgressReporterEndsBeforeTheResultIsPrinted(t *testing.T) {
	f := &fake{status: app.BatchStatus{ID: "b0001", Phase: "running", Counts: map[string]int{"x": 1}}, statusDelay: 400 * time.Millisecond, waitDelay: 800 * time.Millisecond}
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), []string{"run", "--json", "--preset", "p", "--authorize", "--out", t.TempDir(), t.TempDir() + "/a.txt"}, &out, &errOut, opener(f, nil))
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("run did not finish")
	}
	// the process is over: nothing may still write
	n := out.Len()
	time.Sleep(900 * time.Millisecond)
	if out.Len() != n {
		t.Fatal("the progress reporter kept writing after the command ended")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[len(lines)-1], `"event":"result"`) {
		t.Fatalf("the result must be the last line: %q", out.String())
	}
}

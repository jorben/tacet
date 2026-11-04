package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/app"
)

func runArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	return append(append([]string{"run", "--preset", "p", "--authorize", "--out", t.TempDir()}, extra...), t.TempDir()+"/a.txt")
}

func TestRunAndPreflightAskToParkAWaitingTask(t *testing.T) {
	for name, args := range map[string][]string{
		"run":       runArgs(t),
		"preflight": {"preflight", "--out", t.TempDir(), t.TempDir() + "/a.txt"},
	} {
		f := &fake{status: app.BatchStatus{ID: "b0001"}}
		call(context.Background(), opener(f, nil), args...)
		if !f.imported.ParkWaiting {
			t.Errorf("%s did not ask to park: %+v", name, f.imported)
		}
	}
}

func TestAParkedBatchIsNamedOnStandardError(t *testing.T) {
	f := &fake{displaced: "b0007", status: app.BatchStatus{ID: "b0001"}}
	code, out, errOut := call(context.Background(), opener(f, nil), runArgs(t)...)
	if code != ExitOK || !strings.Contains(errOut, "batch b0007") || !strings.Contains(errOut, "tacet list") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if strings.Contains(out, "b0007") {
		t.Fatalf("the notice went to the report: %q", out)
	}
}

// Nothing is said when nothing was parked.
func TestNoNoticeWithoutAParkedBatch(t *testing.T) {
	f := &fake{status: app.BatchStatus{ID: "b0001"}}
	if _, _, errOut := call(context.Background(), opener(f, nil), runArgs(t)...); strings.Contains(errOut, "parked") {
		t.Fatalf("err %q", errOut)
	}
}

// C3: the notice is an event of its own, on the same stream and in the same shape as the others.
func TestAParkedBatchIsAJSONEvent(t *testing.T) {
	f := &fake{displaced: "b0007", status: app.BatchStatus{ID: "b0001", Files: []app.FileStatus{}}}
	_, out, errOut := call(context.Background(), opener(f, nil), runArgs(t, "--json")...)
	if errOut != "" {
		t.Fatalf("err %q", errOut)
	}
	var events []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		events = append(events, ev.Event)
		if ev.Event == "parked" && string(ev.Data) != `{"batch":"b0007"}` {
			t.Fatalf("data %s", ev.Data)
		}
	}
	if got := strings.Join(events, ","); got != "parked,batch,result" {
		t.Fatalf("events %s", got)
	}
}

func TestARefusedImportSaysWhereToLook(t *testing.T) {
	for _, code := range []string{app.CodeBatchActive, app.CodeQuickTaskActive} {
		f := &fake{importErr: errors.New(code)}
		got, _, errOut := call(context.Background(), opener(f, nil), runArgs(t)...)
		if got != ExitUsage || !strings.Contains(errOut, "tacet: "+code) || !strings.Contains(errOut, "tacet list") {
			t.Fatalf("code %d err %q", got, errOut)
		}
		if strings.Contains(strings.Join(f.calls, ","), "start") {
			t.Fatalf("calls %v", f.calls)
		}
	}
}

func TestListShowsEveryUnfinishedBatch(t *testing.T) {
	f := &fake{unfinished: []app.BatchStatus{
		{ID: "b0001", Kind: "batch", Phase: "awaiting_review", Total: 2, Delivered: 1, Parked: true, Counts: map[string]int{"awaiting_review": 1}},
		{ID: "b0002", Kind: "quick", Phase: "processing", Total: 1},
	}}
	code, out, errOut := call(context.Background(), opener(f, nil), "list")
	if code != ExitOK {
		t.Fatalf("code %d err %q", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "b0001") || !strings.Contains(lines[0], "parked") ||
		!strings.Contains(lines[1], "b0002") || !strings.Contains(lines[1], "holds the slot") {
		t.Fatalf("%q", out)
	}
	if _, jsonOut, _ := call(context.Background(), opener(f, nil), "list", "--json"); strings.Count(jsonOut, `"event":"unfinished"`) != 2 || !strings.Contains(jsonOut, `"parked":true`) {
		t.Fatalf("%q", jsonOut)
	}
}

func TestListReportsAFailure(t *testing.T) {
	f := &fake{unfinishedErr: errors.New("STORE_FAILED")}
	if code, _, errOut := call(context.Background(), opener(f, nil), "list"); code != ExitUsage || !strings.Contains(errOut, "STORE_FAILED") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestStatusSaysABatchIsParked(t *testing.T) {
	f := &fake{status: app.BatchStatus{ID: "b0001", Phase: "x", Parked: true}}
	if _, out, _ := call(context.Background(), opener(f, nil), "status", "--batch", "b0001"); !strings.Contains(out, "parked") {
		t.Fatalf("%q", out)
	}
	f.status.Parked = false
	if _, out, _ := call(context.Background(), opener(f, nil), "status", "--batch", "b0001"); strings.Contains(out, "parked") {
		t.Fatalf("%q", out)
	}
}

// C4: a plain clean does not stop anything; it says how to.
func TestCleanOfAnUnfinishedBatchIsRefusedAndNamesTheWayOut(t *testing.T) {
	f := &fake{cleanErr: fmt.Errorf("%w: batch has unfinished files", app.ErrNotAllowed)}
	code, _, errOut := call(context.Background(), opener(f, nil), "clean", "--batch", "b0001")
	if code != ExitUsage || !strings.Contains(errOut, "ACTION_NOT_ALLOWED") || !strings.Contains(errOut, "clean --discard --batch b0001") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "clean,close" {
		t.Fatalf("calls %s", got)
	}
}

func TestCleanWithDiscardStopsTheBatchFirst(t *testing.T) {
	f := &fake{}
	code, out, errOut := call(context.Background(), opener(f, nil), "clean", "--discard", "--batch", "b0001")
	if code != ExitOK || !strings.Contains(out, "cleaned") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if got := strings.Join(f.calls, ","); got != "cancel,resume,wait,clean,close" {
		t.Fatalf("calls %s", got)
	}
}

// The batch was not removed: it is not reported as if it were, whichever step said so.
func TestCleanWithDiscardReportsAFailure(t *testing.T) {
	for name, f := range map[string]*fake{
		"cancel": {cancelErr: errors.New("STOP_FAILED")},
		"clean":  {cleanErr: errors.New("STOP_FAILED")},
	} {
		code, out, errOut := call(context.Background(), opener(f, nil), "clean", "--discard", "--batch", "b0001")
		if code != ExitUsage || strings.Contains(out, "cleaned") || !strings.Contains(errOut, "STOP_FAILED") {
			t.Errorf("%s: code %d out %q err %q", name, code, out, errOut)
		}
	}
}

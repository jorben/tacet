//go:build darwin

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/app"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/testenv"
)

type memKey struct{ b []byte }

func (m *memKey) Load() ([]byte, error) {
	if m.b == nil {
		return nil, keys.ErrKeyNotFound
	}
	return append([]byte(nil), m.b...), nil
}
func (m *memKey) Save(k []byte) error { m.b = append([]byte(nil), k...); return nil }
func (m *memKey) Delete() error       { m.b = nil; return nil }

// rig runs the real CLI over a real engine and real plugin processes.
type rig struct {
	t       *testing.T
	ws, out string
	key     *memKey
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	testenv.Python(t)
	t.Setenv("TACET_ROOT", root)
	return &rig{t: t, ws: filepath.Join(t.TempDir(), "ws"), out: t.TempDir(), key: &memKey{}}
}

func (r *rig) opener(_ context.Context, ws string) (app.Core, error) {
	return app.OpenWith(context.Background(), app.Config{Workspace: ws, UserKey: r.key})
}

func (r *rig) cli(ctx context.Context, args ...string) (int, string, string) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, append([]string{args[0], "--workspace", r.ws}, args[1:]...), &out, &errOut, r.opener)
	}()
	select {
	case code := <-done:
		return code, out.String(), errOut.String()
	case <-time.After(3 * time.Minute):
		r.t.Fatalf("cli %v did not finish", args)
		return 0, "", ""
	}
}

func (r *rig) file(name, body string) string {
	p := filepath.Join(r.t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

const preset = app.PresetID

func TestCLIRunStatusCleanOverARealWorkspace(t *testing.T) {
	r := newRig(t)
	a := r.file("季度联系人.txt", "联系人 13812345678 结束\n无敏感\n")
	b := r.file("b.csv", "name,phone\n张三,13900000000\n")
	code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, a, b)
	if code != ExitOK {
		t.Fatalf("run = %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "季度联系人") || strings.Contains(out+errOut, r.out) || strings.Contains(out+errOut, filepath.Dir(a)) {
		t.Fatalf("output names a source file or path:\n%s%s", out, errOut)
	}
	entries, _ := os.ReadDir(r.out)
	delivered := 0
	for _, en := range entries {
		raw, _ := os.ReadFile(filepath.Join(r.out, en.Name()))
		if strings.Contains(string(raw), "13812345678") || strings.Contains(string(raw), "13900000000") {
			t.Fatalf("%s still holds a number", en.Name())
		}
		if strings.HasPrefix(en.Name(), "b0001-f000") && !strings.HasSuffix(en.Name(), ".json") {
			delivered++
		}
	}
	if delivered != 2 {
		t.Fatalf("delivered %d files: %v", delivered, entries)
	}
	// a later process reads the same verdict from the stored batch
	code, out, errOut = r.cli(context.Background(), "status", "--batch", "b0001")
	if code != ExitOK || !strings.Contains(out, "all_delivered") {
		t.Fatalf("status = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut = r.cli(context.Background(), "clean", "--batch", "b0001"); code != ExitOK {
		t.Fatalf("clean = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, _ = r.cli(context.Background(), "status", "--batch", "b9999"); code != ExitUsage {
		t.Fatalf("unknown batch = %d", code)
	}
}

func TestCLIReviewNeededExitsTwoAndStaysIndependent(t *testing.T) {
	r := newRig(t)
	review := r.file("r.txt", "证件 110101199003074519 与 110101199003074518\n")
	plain := r.file("p.txt", "电话 13812345678\n")
	code, out, errOut := r.cli(context.Background(), "run", "--json", "--preset", preset, "--authorize", "--out", r.out, review, plain)
	if code != ExitIncomplete {
		t.Fatalf("run = %d\n%s\n%s", code, out, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[len(lines)-1], `"awaiting_review"`) || !strings.Contains(lines[len(lines)-1], `"completed_per_policy"`) {
		t.Fatalf("one member should wait and the other be delivered:\n%s", out)
	}
	entries, _ := os.ReadDir(r.out)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if !strings.Contains(strings.Join(names, ","), "b0001-f0002.txt") || strings.Contains(strings.Join(names, ","), "b0001-f0001.txt") {
		t.Fatalf("outputs %v", names)
	}
	if code, _, _ = r.cli(context.Background(), "status", "--batch", "b0001"); code != ExitIncomplete {
		t.Fatalf("status = %d", code)
	}
	if code, _, _ = r.cli(context.Background(), "clean", "--batch", "b0001"); code != ExitUsage {
		t.Fatalf("an unfinished batch must not be cleaned: %d", code)
	}
}

func TestCLIPreflightLeavesNothingAndFlagsUnsupported(t *testing.T) {
	r := newRig(t)
	good := r.file("g.txt", "hello\n")
	bad := r.file("x.unknown", "data")
	code, out, errOut := r.cli(context.Background(), "preflight", good, bad)
	if code != ExitIncomplete || !strings.Contains(out, "unprocessable") || !strings.Contains(out, "preflighted") {
		t.Fatalf("preflight = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut = r.cli(context.Background(), "preflight", good); code != ExitOK {
		t.Fatalf("a clean preflight = %d\n%s\n%s", code, out, errOut)
	}
	// nothing was left to block the next real run
	if code, out, errOut = r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, good); code != ExitOK {
		t.Fatalf("run after preflight = %d\n%s\n%s", code, out, errOut)
	}
}

// A run the core refuses (here: the preset's model is not installed) is over. It says so, and the next run,
// of the same kind, starts: the draft stands in nobody's way, and no start request is left to wait for.
func TestCLIARefusedRunLeavesNothingBehind(t *testing.T) {
	t.Setenv("TACET_PREINSTALLED_MODELS", "")
	for _, tc := range []struct {
		name  string
		flags []string
	}{{"batch", nil}, {"quick", []string{"--quick"}}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			good := r.file("g.txt", "电话 13812345678\n")
			run := func(preset string) (int, string, string) {
				args := append([]string{"run", "--preset", preset, "--authorize", "--out", r.out}, tc.flags...)
				return r.cli(context.Background(), append(args, good)...)
			}
			code, out, errOut := run(app.ModelPresetID)
			if code != ExitUsage || !strings.Contains(errOut, "tacet: NOT_READY") {
				t.Fatalf("refused run = %d\n%s\n%s", code, out, errOut)
			}
			r.expectNothingLeft("b0001")
			if code, out, errOut = run(preset); code != ExitOK {
				t.Fatalf("run after the refused one = %d\n%s\n%s", code, out, errOut)
			}
		})
	}
}

// expectNothingLeft checks the state a refused run must not leave, through a later process's view of the
// workspace: no batch listed, no data of its files, no start request kept for it.
func (r *rig) expectNothingLeft(batch string) {
	r.t.Helper()
	core, err := r.opener(context.Background(), r.ws)
	if err != nil {
		r.t.Fatal(err)
	}
	defer core.Close()
	left, err := core.Unfinished(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	if len(left) != 0 {
		r.t.Errorf("a batch is still listed: %+v", left)
	}
	if kept, _ := os.ReadDir(filepath.Join(r.ws, "resources")); len(kept) != 0 {
		r.t.Errorf("the inspection left %d resource(s) of the files", len(kept))
	}
	// A kept start request cannot be read from here, but Wait does not return while one is there. Nothing else
	// of this batch is running, so the wait takes a few reads, or never ends: the deadline only tells them apart.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = core.Wait(ctx, batch); errors.Is(err, context.DeadlineExceeded) {
		r.t.Errorf("a wait for the batch does not end: its start request (run.Pending) was left")
	}
}

// discard relies on this: a cancel drops the start the draft kept, so a wait ends without anything carrying it out,
// and a resume begins nothing even where that start could go through (this preset needs no model, as when a model
// was installed just after the refusal).
func TestACancelledDraftIsNotBegunByTheStartItKept(t *testing.T) {
	r := newRig(t)
	bg := context.Background()
	core, err := r.opener(bg, r.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	st, err := core.Import(bg, app.ImportRequest{Kind: "batch", Files: []string{r.file("g.txt", "电话 13812345678\n")}, OutputDir: r.out, Preset: preset})
	if err != nil {
		t.Fatal(err)
	}
	// like a refused start, this one reaches the core with the inspection over
	if _, err = core.Preflight(bg, st.ID); err != nil {
		t.Fatal(err)
	}
	ended, end := context.WithCancel(bg)
	end() // the call ends, the request stays on record
	if err = core.Start(ended, app.StartRequest{Batch: st.ID, Preset: preset, OutputDir: r.out, Authorize: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = core.Cancel(bg, st.ID); err != nil {
		t.Fatal(err)
	}
	// Nothing else of this batch is running, so the wait takes a few reads or never ends: the deadline only tells them apart.
	ctx, cancel := context.WithTimeout(bg, 30*time.Second)
	defer cancel()
	if _, err = core.Wait(ctx, st.ID); err != nil {
		t.Fatalf("the wait after the cancel: %v", err)
	}
	if err = core.Resume(bg, st.ID); err != nil {
		t.Fatal(err)
	}
	res, err := core.Wait(ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || res.Files[0].State != "cancelled" {
		t.Fatalf("the draft began: %+v", res)
	}
	if delivered, _ := os.ReadDir(r.out); len(delivered) != 0 {
		t.Fatalf("delivered %v", delivered)
	}
}

func TestCLIWorkspaceBusyAndMissingKeyAreExplicit(t *testing.T) {
	r := newRig(t)
	good := r.file("g.txt", "hello\n")
	core, err := r.opener(context.Background(), r.ws)
	if err != nil {
		t.Fatal(err)
	}
	code, _, errOut := r.cli(context.Background(), "status", "--batch", "b0001")
	if code != ExitUsage || !strings.Contains(errOut, "WORKSPACE_BUSY") {
		t.Fatalf("second writer = %d %q", code, errOut)
	}
	core.Close()
	if code, _, errOut = r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, good); code != ExitOK {
		t.Fatalf("run = %d %s", code, errOut)
	}
	r.key.b = nil // the platform key is gone: no prompt, no fallback
	code, _, errOut = r.cli(context.Background(), "status", "--batch", "b0001")
	if code != ExitUsage || strings.Contains(errOut, "INTERNAL_ERROR") {
		t.Fatalf("missing key = %d %q", code, errOut)
	}
	t.Logf("missing key reported as: %s", strings.TrimSpace(errOut))
}

func TestCLIInterruptPausesAndResumeFinishes(t *testing.T) {
	r := newRig(t)
	var paths []string
	for i := 0; i < 6; i++ {
		paths = append(paths, r.file("f"+string(rune('a'+i))+".txt", "电话 13812345678\n"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(1200 * time.Millisecond); cancel() }()
	code, out, errOut := r.cli(ctx, append([]string{"run", "--preset", preset, "--authorize", "--out", r.out}, paths...)...)
	if code != ExitInterrupted {
		t.Skipf("the batch finished before the interrupt (%d); nothing to resume\n%s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "resume --batch b0001") {
		t.Fatalf("no resume hint: %q", errOut)
	}
	code, out, errOut = r.cli(context.Background(), "resume", "--batch", "b0001")
	if code != ExitOK {
		t.Fatalf("resume = %d\n%s\n%s", code, out, errOut)
	}
}

// scripted is a terminal a test types on.
type scripted struct {
	interactive bool
	answers     []string
	prompts     []string
}

func (s *scripted) Interactive() bool { return s.interactive }
func (s *scripted) Password(_ context.Context, prompt string) ([]byte, error) {
	s.prompts = append(s.prompts, prompt)
	if len(s.answers) == 0 {
		return nil, nil
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return []byte(a), nil
}

func (r *rig) cliOn(term Terminal, args ...string) (int, string, string) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	code := RunWith(context.Background(), append([]string{args[0], "--workspace", r.ws}, args[1:]...), &out, &errOut, r.opener, term)
	return code, out.String(), errOut.String()
}

const encryptedDocx = `
import sys
sys.path.insert(0, sys.argv[1])
from test_docx_extract import package
from test_docx_unlock import encrypted, sized
open(sys.argv[2], 'wb').write(encrypted(sized(package('<w:p><w:r><w:t>电话 13812345678</w:t></w:r></w:p>')), 'correct horse'))
`

// FR-09 and 3.6: a file that needs a password is reported as blocked by a non-interactive run, with the way
// to unlock it; "unlock" reads the password on an interactive terminal only, asks again after a wrong one,
// leaves a file for which nothing was typed, and never shows a password.
func TestCLIUnlockReadsPasswordsOnATerminalOnly(t *testing.T) {
	r := newRig(t)
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	py := testenv.Python(t)
	locked := filepath.Join(t.TempDir(), "enc.docx")
	if o, err := exec.Command(py, "-X", "utf8", "-c", encryptedDocx, filepath.Join(root, "python", "tests"), locked).CombinedOutput(); err != nil {
		testenv.Missing(t, "the fixture, which the plugin interpreter builds: %v %s", err, o)
	}
	plain := r.file("a.txt", "电话 13812345678\n")
	code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, plain, locked)
	if code != ExitIncomplete || !strings.Contains(errOut, "tacet unlock --batch b0001") {
		t.Fatalf("run = %d\n%s\n%s", code, out, errOut)
	}
	// not a terminal: nothing is read
	pipe := &scripted{answers: []string{"correct horse"}}
	if code, _, errOut = r.cliOn(pipe, "unlock", "--batch", "b0001"); code != ExitUsage || !strings.Contains(errOut, "TERMINAL_REQUIRED") || len(pipe.prompts) != 0 {
		t.Fatalf("unlock without a terminal = %d %q %v", code, errOut, pipe.prompts)
	}
	// an empty line leaves the file, and it is not asked for again in this run
	skip := &scripted{interactive: true, answers: []string{""}}
	if code, _, _ = r.cliOn(skip, "unlock", "--batch", "b0001"); code != ExitIncomplete || len(skip.prompts) != 1 {
		t.Fatalf("left = %d %v", code, skip.prompts)
	}
	// a wrong password is asked for again; the right one unlocks and the batch finishes
	term := &scripted{interactive: true, answers: []string{"wrong horse", "correct horse"}}
	code, out, errOut = r.cliOn(term, "unlock", "--batch", "b0001")
	if code != ExitOK || len(term.prompts) != 2 || !strings.Contains(errOut, "was not accepted") {
		t.Fatalf("unlock = %d %v\n%s\n%s", code, term.prompts, out, errOut)
	}
	if !strings.Contains(term.prompts[0], "f0002") || strings.Contains(term.prompts[0], "enc.docx") {
		t.Fatalf("the prompt names the file by its neutral name only: %q", term.prompts[0])
	}
	if strings.Contains(out+errOut, "horse") {
		t.Fatalf("a password was shown:\n%s\n%s", out, errOut)
	}
	if !strings.Contains(out, "2 delivered") {
		t.Fatalf("%s", out)
	}
	// two locked files: the one left with an empty line is not asked for again when the other one's
	// wrong password is asked for again
	r2 := newRig(t)
	other := filepath.Join(t.TempDir(), "enc2.docx")
	if o, err := exec.Command(py, "-X", "utf8", "-c", encryptedDocx, filepath.Join(root, "python", "tests"), other).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, o)
	}
	if code, out, errOut = r2.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r2.out, locked, other); code != ExitIncomplete {
		t.Fatalf("run = %d\n%s\n%s", code, out, errOut)
	}
	two := &scripted{interactive: true, answers: []string{"", "wrong horse", "correct horse"}}
	code, out, _ = r2.cliOn(two, "unlock", "--batch", "b0001")
	if len(two.prompts) != 3 || !strings.Contains(two.prompts[0], "f0001") || !strings.Contains(two.prompts[1], "f0002") || !strings.Contains(two.prompts[2], "f0002") {
		t.Fatalf("asked %v", two.prompts)
	}
	if code != ExitIncomplete || !strings.Contains(out, "1 delivered") {
		t.Fatalf("unlock = %d\n%s", code, out)
	}
}

// C1, C2, C4: a run with a file left to review does not stop the next one; the batch it leaves is still there.
func TestCLIARunWaitingForReviewDoesNotStopTheNextRun(t *testing.T) {
	r := newRig(t)
	review := r.file("r.txt", "证件 110101199003074519 与 110101199003074518\n")
	plain := r.file("p.txt", "电话 13812345678\n")
	if code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, review); code != ExitIncomplete {
		t.Fatalf("first run = %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, plain)
	if code != ExitOK || !strings.Contains(out, "batch b0002 created") {
		t.Fatalf("second run = %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "b0001") || !strings.Contains(errOut, "parked") {
		t.Fatalf("the second run did not name the parked batch: %q", errOut)
	}
	code, out, _ = r.cli(context.Background(), "status", "--batch", "b0001")
	if code != ExitIncomplete || !strings.Contains(out, "parked") {
		t.Fatalf("status = %d\n%s", code, out)
	}
	// every later process can find it
	code, out, _ = r.cli(context.Background(), "list")
	if code != ExitOK || !strings.Contains(out, "b0001") || !strings.Contains(out, "parked") || strings.Contains(out, "b0002") {
		t.Fatalf("list = %d\n%s", code, out)
	}
	code, _, errOut = r.cli(context.Background(), "clean", "--batch", "b0001")
	if code != ExitUsage || !strings.Contains(errOut, "--discard") {
		t.Fatalf("clean of a parked batch = %d\n%s", code, errOut)
	}
	code, out, errOut = r.cli(context.Background(), "clean", "--discard", "--batch", "b0001")
	if code != ExitOK {
		t.Fatalf("clean --discard = %d\n%s\n%s", code, out, errOut)
	}
	if code, out, _ = r.cli(context.Background(), "list"); code != ExitOK || strings.Contains(out, "b0001") {
		t.Fatalf("list after discard = %d\n%s", code, out)
	}
	// b0002 keeps its own data until it is cleaned; once it is, nothing of b0001 may be left either
	if code, out, errOut = r.cli(context.Background(), "clean", "--batch", "b0002"); code != ExitOK {
		t.Fatalf("clean b0002 = %d\n%s\n%s", code, out, errOut)
	}
	if kept, _ := os.ReadDir(filepath.Join(r.ws, "resources")); len(kept) != 0 {
		t.Fatalf("the discarded batch left %d resource(s)", len(kept))
	}
}

// Several in a row: none is lost and none has to be dealt with first.
func TestCLIParkedBatchesAddUp(t *testing.T) {
	r := newRig(t)
	review := r.file("r.txt", "证件 110101199003074519 与 110101199003074518\n")
	for i := range 3 {
		if code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, review); code != ExitIncomplete {
			t.Fatalf("run %d = %d\n%s\n%s", i, code, out, errOut)
		}
	}
	_, out, _ := r.cli(context.Background(), "list")
	for _, id := range []string{"b0001", "b0002", "b0003"} {
		if !strings.Contains(out, id) {
			t.Fatalf("%s is not listed:\n%s", id, out)
		}
	}
}

// C3 with the real engine: the stream stays one JSON object per line.
func TestCLIJSONReportsTheParkedBatchAsAnEvent(t *testing.T) {
	r := newRig(t)
	review := r.file("r.txt", "证件 110101199003074519 与 110101199003074518\n")
	if code, out, errOut := r.cli(context.Background(), "run", "--preset", preset, "--authorize", "--out", r.out, review); code != ExitIncomplete {
		t.Fatalf("first run = %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut := r.cli(context.Background(), "run", "--json", "--preset", preset, "--authorize", "--out", r.out, r.file("p.txt", "电话 13812345678\n"))
	if code != ExitOK || !strings.Contains(out, `{"data":{"batch":"b0001"},"event":"parked"}`) {
		t.Fatalf("run = %d\n%s\n%s", code, out, errOut)
	}
}

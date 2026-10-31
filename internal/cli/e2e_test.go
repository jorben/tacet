//go:build darwin

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/app"
	"github.com/jorben/tacet/internal/keys"
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
	if _, err := os.Stat(filepath.Join(root, "python", ".venv", "bin", "python")); err != nil {
		t.Skip("needs uv sync --all-packages")
	}
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

// Package cli is the non-interactive front end of the shared core. It parses
// arguments, calls app.Core and prints the core's verdicts; it holds no
// judgement of its own, so its exit code and the desktop's status agree (AR-10).
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jorben/tacet/internal/app"
)

// Exit codes of requirements 3.6.
const (
	ExitOK          = 0 // every requested file was published per policy
	ExitUsage       = 1 // usage error, or the batch cannot start
	ExitIncomplete  = 2 // something is awaiting review, blocked, failed, partial or undelivered
	ExitInterrupted = 3 // interrupted by the user
)

var commands = []struct{ name, summary string }{
	{"preflight", "Check files without processing them"},
	{"run", "Process a file list with an explicit policy and publish authorization"},
	{"status", "Show batch status and the per-file report"},
	{"list", "List the batches that are not finished, parked ones included"},
	{"resume", "Resume a batch with its original snapshot"},
	{"retry", "Retry selected members"},
	{"unlock", "Enter the passwords of a batch's files on this terminal"},
	{"clean", "Clean up task data (--discard also stops a batch that is not finished)"},
}

// Opener opens the core over a workspace directory.
type Opener func(ctx context.Context, workspace string) (app.Core, error)

// DefaultOpener opens the engine with the platform credential store.
func DefaultOpener(ctx context.Context, workspace string) (app.Core, error) {
	return app.OpenWith(ctx, app.Config{Workspace: workspace})
}

// Main runs the CLI and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), app.Interrupts()...)
	defer stop()
	return Run(ctx, args, stdout, stderr, DefaultOpener)
}

type env struct {
	ctx    context.Context
	out    io.Writer
	err    io.Writer
	open   Opener
	term   Terminal
	asJSON bool
}

// Terminal is where a person types passwords: never echoed, never from arguments or the environment.
type Terminal interface {
	Interactive() bool
	Password(ctx context.Context, prompt string) ([]byte, error)
}

// Run is Main with an explicit context (cancelled by the user's interrupt) and opener.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, open Opener) int {
	return RunWith(ctx, args, stdout, stderr, open, app.StdinTerminal{})
}

// RunWith is Run with the terminal passwords are read from.
func RunWith(ctx context.Context, args []string, stdout, stderr io.Writer, open Opener, term Terminal) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	e := &env{ctx: ctx, out: stdout, err: stderr, open: open, term: term}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return ExitOK
	case "version", "--version":
		fmt.Fprintf(stdout, "%s %s\n", app.Name, app.Version)
		return ExitOK
	case "preflight":
		return e.preflight(args[1:])
	case "run":
		return e.run(args[1:])
	case "status":
		return e.status(args[1:])
	case "list":
		return e.list(args[1:])
	case "resume":
		return e.resume(args[1:])
	case "retry":
		return e.retry(args[1:])
	case "clean":
		return e.clean(args[1:])
	case "unlock":
		return e.unlock(args[1:])
	}
	fmt.Fprintf(stderr, "tacet: unknown command %q\n", args[0])
	usage(stderr)
	return ExitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: tacet <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "  %-10s %s\n", "version", "Print the version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit status: 0 all published, 1 usage error or cannot start, 2 something needs review/blocked/failed/partial/undelivered, 3 interrupted")
	fmt.Fprintln(w, "Passwords are never taken from arguments or the environment; files that need one are reported as blocked")
	fmt.Fprintln(w, "and can be unlocked with \"tacet unlock --batch <id>\" on an interactive terminal (input is not shown).")
}

// -- common options ----------------------------------------------------------

type common struct {
	workspace string
	batch     string
	json      bool
}

func (e *env) flags(name string, c *common, batch bool) *flag.FlagSet {
	fs := flag.NewFlagSet("tacet "+name, flag.ContinueOnError)
	fs.SetOutput(e.err)
	fs.StringVar(&c.workspace, "workspace", "", "workspace directory (default: the CLI's own, independent of the desktop app)")
	fs.BoolVar(&c.json, "json", false, "structured output: one JSON object per line")
	if batch {
		fs.StringVar(&c.batch, "batch", "", "batch id")
	}
	return fs
}

func (e *env) fail(code string, detail string) int {
	if e.asJSON {
		b, _ := json.Marshal(map[string]string{"event": "error", "code": code, "detail": detail})
		fmt.Fprintln(e.err, string(b))
	} else if detail != "" {
		fmt.Fprintf(e.err, "tacet: %s: %s\n", code, detail)
	} else {
		fmt.Fprintf(e.err, "tacet: %s\n", code)
	}
	return ExitUsage
}

// codeOf names an error for the user: closed codes only, never content or paths.
func codeOf(err error) string {
	s := err.Error()
	if i := strings.Index(s, ":"); i > 0 {
		s = s[:i]
	}
	if s != "" && s == strings.ToUpper(s) && !strings.ContainsAny(s, " /\\") {
		return s
	}
	return "INTERNAL_ERROR"
}

func (e *env) openCore(c common) (app.Core, int) {
	ws := c.workspace
	var err error
	if ws == "" {
		if ws, err = app.DefaultWorkspace("cli"); err != nil {
			return nil, e.fail("WORKSPACE_UNAVAILABLE", "")
		}
	}
	if ws, err = filepath.Abs(ws); err != nil {
		return nil, e.fail("WORKSPACE_UNAVAILABLE", "")
	}
	if err = os.MkdirAll(filepath.Dir(ws), 0o700); err != nil {
		return nil, e.fail("WORKSPACE_UNAVAILABLE", "")
	}
	core, err := e.open(e.ctx, ws)
	if err != nil {
		return nil, e.fail(codeOf(err), "")
	}
	return core, 0
}

// files collects the operands and the list file (one path per line; "-" is stdin).
func files(list string, operands []string, stdin io.Reader) ([]string, error) {
	var out []string
	add := func(p string) error {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		out = append(out, abs)
		return nil
	}
	for _, p := range operands {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	if list != "" {
		r := stdin
		if list != "-" {
			f, err := os.Open(list)
			if err != nil {
				return nil, errors.New("LIST_UNREADABLE")
			}
			defer f.Close()
			r = f
		}
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if err := add(line); err != nil {
				return nil, err
			}
		}
		if sc.Err() != nil {
			return nil, errors.New("LIST_UNREADABLE")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("NO_FILES")
	}
	return out, nil
}

// -- output ------------------------------------------------------------------

func (e *env) emit(event string, v any) {
	if !e.asJSON {
		return
	}
	b, _ := json.Marshal(map[string]any{"event": event, "data": v})
	fmt.Fprintln(e.out, string(b))
}

func (e *env) print(st app.BatchStatus) {
	if e.asJSON {
		e.emit("result", st)
		return
	}
	parked := ""
	if st.Parked {
		parked = ", parked"
	}
	fmt.Fprintf(e.out, "batch %s: %s%s, %d file(s), %d delivered, exit %d\n", st.ID, st.Phase, parked, st.Total, st.Delivered, st.ExitCode)
	for _, f := range st.Files {
		line := fmt.Sprintf("  %s  %-24s", f.Name, f.Status)
		if f.State != f.Status {
			line += " state=" + f.State
		}
		if f.Detail != "" {
			line += " reason=" + f.Detail
		}
		if f.Pending > 0 {
			line += fmt.Sprintf(" pending=%d", f.Pending)
		}
		if f.Output != "" {
			line += " output=" + f.Output
		}
		fmt.Fprintln(e.out, line)
	}
	if st.Truncated {
		fmt.Fprintln(e.out, "  ... (list truncated; use --json for the full report)")
	}
}

// follow waits for the batch while reporting changes of its progress.
func (e *env) follow(core app.Core, batch string) (app.BatchStatus, error) {
	done := make(chan struct{})
	exited := make(chan struct{})
	// The progress reporter writes to the same output as the result: it must be gone
	// before follow returns, or its last line could interleave with the final one.
	defer func() { close(done); <-exited }()
	if !e.asJSON {
		close(exited)
	} else {
		go func() {
			defer close(exited)
			var last string
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					st, err := core.Status(context.Background(), batch, 0)
					if err != nil {
						continue
					}
					if key := fmt.Sprint(st.Phase, st.Counts); key != last {
						last = key
						e.emit("progress", map[string]any{"batch": st.ID, "phase": st.Phase, "counts": st.Counts, "total": st.Total})
					}
				}
			}
		}()
	}
	return core.Wait(e.ctx, batch)
}

// finish turns the outcome of a wait into the exit status; an interrupt pauses
// the batch so that "resume" continues exactly where it stopped.
func (e *env) finish(core app.Core, batch string, st app.BatchStatus, err error) int {
	switch {
	case err == nil:
		e.print(st)
		if !e.asJSON && len(passwordWaiting(st.Files, nil)) > 0 {
			fmt.Fprintf(e.err, "tacet: some files need a password; enter them with: tacet unlock --batch %s\n", batch)
		}
		return st.ExitCode
	case errors.Is(err, context.Canceled):
		core.Pause(context.Background(), batch)
		if e.asJSON {
			e.emit("interrupted", map[string]string{"batch": batch})
		} else {
			fmt.Fprintf(e.err, "tacet: interrupted; continue with: tacet resume --batch %s\n", batch)
		}
		return ExitInterrupted
	}
	return e.fail(codeOf(err), "")
}

// -- commands ----------------------------------------------------------------

type input struct {
	common
	list      string
	out       string
	preset    string
	authorize bool
	partial   bool
	quick     bool
}

func (e *env) inputFlags(name string) (*flag.FlagSet, *input) {
	in := &input{}
	fs := e.flags(name, &in.common, false)
	fs.StringVar(&in.list, "list", "", "file holding one input path per line (- for stdin)")
	fs.StringVar(&in.out, "out", "", "output directory (must exist)")
	fs.StringVar(&in.preset, "preset", "", "run preset id (the policy)")
	fs.BoolVar(&in.quick, "quick", false, "treat the single file as a quick task")
	return fs, in
}

// imported creates the batch of a command, making room if the task of this kind only waits for the person.
func (e *env) imported(core app.Core, in *input, list []string) (app.BatchStatus, error) {
	st, err := core.Import(e.ctx, app.ImportRequest{Kind: kindOf(in.quick, len(list)), Files: list, OutputDir: in.out, Preset: in.preset, ParkWaiting: true})
	if err == nil && st.Displaced != "" {
		e.emit("parked", map[string]string{"batch": st.Displaced})
		if !e.asJSON {
			fmt.Fprintf(e.err, "tacet: batch %s still waits for a person and was parked; see: tacet list\n", st.Displaced)
		}
	}
	return st, err
}

// failImport reports a refused import. A slot refused here is held by a task with work under way or a draft: a
// task that only waited for a person would have been parked.
func (e *env) failImport(err error) int {
	code := codeOf(err)
	if code == app.CodeBatchActive || code == app.CodeQuickTaskActive {
		return e.fail(code, "a task of this kind is being worked on or has not started; see: tacet list")
	}
	return e.fail(code, "")
}

func (e *env) preflight(args []string) int {
	fs, in := e.inputFlags("preflight")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = in.json
	list, err := files(in.list, fs.Args(), os.Stdin)
	if err != nil {
		return e.fail(codeOf(err), "")
	}
	if in.out == "" {
		in.out = os.TempDir()
	}
	core, code := e.openCore(in.common)
	if code != 0 {
		return code
	}
	defer core.Close()
	st, err := e.imported(core, in, list)
	if err != nil {
		return e.failImport(err)
	}
	if st, err = core.Preflight(e.ctx, st.ID); err != nil {
		return e.fail(codeOf(err), "")
	}
	// a preflight leaves nothing behind: the draft is cancelled and cleaned
	ok := true
	for _, f := range st.Files {
		if f.State != "preflighted" {
			ok = false
		}
	}
	core.Cancel(context.Background(), st.ID)
	core.Clean(context.Background(), st.ID)
	e.print(st)
	if !ok || st.Truncated {
		return ExitIncomplete
	}
	return ExitOK
}

func kindOf(quick bool, n int) string {
	if quick && n == 1 {
		return "quick"
	}
	return "batch"
}

func (e *env) run(args []string) int {
	fs, in := e.inputFlags("run")
	fs.BoolVar(&in.authorize, "authorize", false, "authorize publishing every file the policy lets through")
	fs.BoolVar(&in.partial, "allow-partial", false, "also authorize marked partial results")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = in.json
	switch {
	case in.preset == "":
		return e.fail("POLICY_REQUIRED", "name the policy with --preset")
	case !in.authorize:
		return e.fail("AUTHORIZATION_REQUIRED", "a non-interactive run needs an explicit --authorize")
	case in.out == "":
		return e.fail("OUTPUT_REQUIRED", "name the output directory with --out")
	}
	list, err := files(in.list, fs.Args(), os.Stdin)
	if err != nil {
		return e.fail(codeOf(err), "")
	}
	if in.out, err = filepath.Abs(in.out); err != nil {
		return e.fail("OUTPUT_REQUIRED", "")
	}
	core, code := e.openCore(in.common)
	if code != 0 {
		return code
	}
	defer core.Close()
	st, err := e.imported(core, in, list)
	if err != nil {
		return e.failImport(err)
	}
	e.emit("batch", map[string]any{"batch": st.ID, "total": st.Total})
	if !e.asJSON {
		fmt.Fprintf(e.out, "batch %s created\n", st.ID)
	}
	if err = core.Start(e.ctx, app.StartRequest{Batch: st.ID, Preset: in.preset, OutputDir: in.out, Authorize: true, AllowPartial: in.partial}); err != nil {
		// An interrupt does not always come back as context.Canceled: a read of Start's transaction that it cuts
		// off is reported as what that read was for (NOT_FOUND). Whatever Start says, the run was interrupted.
		if errors.Is(err, context.Canceled) || e.ctx.Err() != nil {
			return e.finish(core, st.ID, app.BatchStatus{}, context.Canceled)
		}
		// The refusal is the outcome: it is reported before discarding, which waits and must not hide it.
		code := e.fail(codeOf(err), "")
		_ = e.discard(core, st.ID) // the run has failed already; what is left can be cleaned later
		return code
	}
	res, err := e.follow(core, st.ID)
	return e.finish(core, st.ID, res, err)
}

// discard stops a batch and removes what the workspace holds for it: the draft of a run whose start the core
// refused, or a batch the person gives up (clean --discard). The core keeps a refused draft, for a caller that
// can start it again once the cause is gone (the desktop app); this command is over, so it must not go on
// holding the batch slot.
func (e *env) discard(core app.Core, batch string) error {
	bg := context.Background()
	if err := core.Cancel(bg, batch); err != nil {
		return err // files still in the draft are what Resume below could begin
	}
	// The refused start stays on record (run.Pending) and a later Wait would never end on it. Only Resume drops
	// it: it carries the start out, which cancelled files refuse, and then drops it. Wait sees that happen. A
	// Resume that fails has not dropped it, so nothing would end that wait, and the record stays. With no attempt
	// running it holds no slot (Cancel finished the batch) and the files can still be cleaned. With one running,
	// its file is still Cancelling: the FinishBatch in Cancel got ErrBatchActive, which is ignored, so the batch is
	// not finished and Clean below is refused.
	if core.Resume(bg, batch) == nil {
		core.Wait(e.ctx, batch)
	}
	return core.Clean(bg, batch)
}

func (e *env) status(args []string) int {
	var c common
	fs := e.flags("status", &c, true)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	if c.batch == "" {
		return e.fail("BATCH_REQUIRED", "name the batch with --batch")
	}
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	st, err := core.Status(e.ctx, c.batch, 200)
	if err != nil {
		return e.fail(codeOf(err), "")
	}
	e.print(st)
	return st.ExitCode
}

// list shows every batch that is not finished, so a parked one can be found again. Listing succeeded whatever
// the batches wait for: their own verdicts are read with status.
func (e *env) list(args []string) int {
	var c common
	fs := e.flags("list", &c, false)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	bs, err := core.Unfinished(e.ctx)
	if err != nil {
		return e.fail(codeOf(err), "")
	}
	for _, b := range bs {
		if e.asJSON {
			e.emit("unfinished", b)
			continue
		}
		held := "holds the slot"
		if b.Parked {
			held = "parked"
		}
		fmt.Fprintf(e.out, "batch %s: %s, %s, %s, %d file(s), %d delivered\n", b.ID, b.Kind, held, b.Phase, b.Total, b.Delivered)
	}
	return ExitOK
}

func (e *env) resume(args []string) int {
	var c common
	fs := e.flags("resume", &c, true)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	if c.batch == "" {
		return e.fail("BATCH_REQUIRED", "name the batch with --batch")
	}
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	if err := core.Resume(e.ctx, c.batch); err != nil {
		return e.fail(codeOf(err), "")
	}
	st, err := e.follow(core, c.batch)
	return e.finish(core, c.batch, st, err)
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func (e *env) retry(args []string) int {
	var c common
	var only multi
	fs := e.flags("retry", &c, true)
	fs.Var(&only, "file", "member to retry (repeatable; default: every failed or blocked member)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	if c.batch == "" {
		return e.fail("BATCH_REQUIRED", "name the batch with --batch")
	}
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	if err := core.Retry(e.ctx, c.batch, only); err != nil {
		return e.fail(codeOf(err), "")
	}
	st, err := e.follow(core, c.batch)
	return e.finish(core, c.batch, st, err)
}

func (e *env) clean(args []string) int {
	var c common
	var stop bool
	fs := e.flags("clean", &c, true)
	fs.BoolVar(&stop, "discard", false, "also stop a batch that is not finished; what it has not delivered is lost")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	if c.batch == "" {
		return e.fail("BATCH_REQUIRED", "name the batch with --batch")
	}
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	if stop {
		if err := e.discard(core, c.batch); err != nil {
			return e.fail(codeOf(err), "")
		}
	} else if err := core.Clean(e.ctx, c.batch); err != nil {
		if errors.Is(err, app.ErrNotAllowed) {
			return e.fail(codeOf(err), fmt.Sprintf("the batch is not finished; to stop and clean it: tacet clean --discard --batch %s", c.batch))
		}
		return e.fail(codeOf(err), "")
	}
	if e.asJSON {
		e.emit("cleaned", map[string]string{"batch": c.batch})
	} else {
		fmt.Fprintf(e.out, "batch %s cleaned\n", c.batch)
	}
	return ExitOK
}

// passwordWaiting lists the files that wait for a password (optionally only those named, by id or name).
func passwordWaiting(fs []app.FileStatus, only map[string]bool) []app.FileStatus {
	var out []app.FileStatus
	for _, f := range fs {
		waits := f.State == "locked" || (f.State == "blocked" && (f.Detail == "password_required" || f.Detail == "PASSWORD_INCORRECT"))
		if waits && (len(only) == 0 || only[f.ID] || only[f.Name]) {
			out = append(out, f)
		}
	}
	return out
}

// maxUnlockRounds bounds how often a wrong password is asked for again.
const maxUnlockRounds = 3

// unlock asks, on an interactive terminal, for the password of each file of the batch that waits for one
// (FR-09), then waits for the batch. Input is not echoed; an empty line leaves that file blocked. A wrong
// password is asked for again, up to maxUnlockRounds. Without a terminal nothing is read: passwords are
// never taken from a pipe, an argument or the environment.
func (e *env) unlock(args []string) int {
	var c common
	var only multi
	fs := e.flags("unlock", &c, true)
	fs.Var(&only, "file", "file id or name (repeatable); default: every file that waits for a password")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	e.asJSON = c.json
	if c.batch == "" {
		return e.fail("BATCH_REQUIRED", "name the batch with --batch")
	}
	if e.term == nil || !e.term.Interactive() {
		return e.fail("TERMINAL_REQUIRED", "passwords are entered on an interactive terminal only")
	}
	core, code := e.openCore(c)
	if code != 0 {
		return code
	}
	defer core.Close()
	want := map[string]bool{}
	for _, f := range only {
		want[f] = true
	}
	// opening the workspace pauses what the last run left unfinished; the batch continues from here, so a
	// file waiting for its password is waiting again
	if err := core.Resume(e.ctx, c.batch); err != nil {
		return e.fail(codeOf(err), "")
	}
	st, err := core.Status(e.ctx, c.batch, 200)
	if err != nil {
		return e.fail(codeOf(err), "")
	}
	left := map[string]bool{}
	for round := 0; round < maxUnlockRounds; round++ {
		var waiting []app.FileStatus
		for _, f := range passwordWaiting(st.Files, want) {
			if !left[f.ID] {
				waiting = append(waiting, f)
			}
		}
		if len(waiting) == 0 {
			if round == 0 {
				fmt.Fprintln(e.err, "tacet: no file of this batch waits for a password")
			}
			break
		}
		asked := 0
		for _, f := range waiting {
			if f.Detail == "PASSWORD_INCORRECT" {
				fmt.Fprintf(e.err, "tacet: the password of %s was not accepted\n", f.Name)
			}
			pw, err := e.term.Password(e.ctx, fmt.Sprintf("Password for %s (not shown, empty to leave it): ", f.Name))
			if err != nil {
				clear(pw)
				if e.ctx.Err() != nil {
					return e.finish(core, c.batch, st, e.ctx.Err())
				}
				return e.fail(codeOf(err), "")
			}
			if len(pw) == 0 {
				left[f.ID] = true // not asked again in this run
				continue
			}
			err = core.Unlock(e.ctx, f.ID, pw)
			clear(pw)
			if err != nil {
				return e.fail(codeOf(err), "")
			}
			asked++
		}
		if st, err = e.follow(core, c.batch); err != nil {
			return e.finish(core, c.batch, st, err)
		}
		if asked == 0 {
			break
		}
	}
	return e.finish(core, c.batch, st, nil)
}

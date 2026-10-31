//go:build darwin || windows

package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

type fakeTxn struct {
	b         *fakeBackend
	hooks     []func()
	events    int
	committed bool
	rolled    bool
}

func (t *fakeTxn) AfterCommit(fn func()) { t.hooks = append(t.hooks, fn) }
func (t *fakeTxn) Emit(store.Event)      { t.events++ }
func (t *fakeTxn) Rollback() error       { t.rolled = true; t.b.rollbacks.Add(1); return nil }
func (t *fakeTxn) Commit() error {
	if t.b.failCommit {
		return errors.New("disk full")
	}
	t.committed = true
	t.b.commits.Add(1)
	return nil
}

type fakeBackend struct {
	active             atomic.Int32
	maxActive          atomic.Int32
	commits, rollbacks atomic.Int32
	failCommit         bool
}

func (b *fakeBackend) Begin(context.Context) (Txn, error) {
	n := b.active.Add(1)
	for {
		m := b.maxActive.Load()
		if n <= m || b.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	return &endTxn{fakeTxn: &fakeTxn{b: b}}, nil
}

// endTxn marks the transaction inactive on commit or rollback.
type endTxn struct{ *fakeTxn }

func (t *endTxn) Commit() error {
	defer t.b.active.Add(-1)
	err := t.fakeTxn.Commit()
	if err == nil {
		for _, h := range t.hooks {
			_ = h
		}
	}
	return err
}
func (t *endTxn) Rollback() error { defer t.b.active.Add(-1); return t.fakeTxn.Rollback() }

type versions map[string][2]any

func (v versions) Current(_ store.Tx, id string) (string, uint64, bool) {
	e, ok := v[id]
	if !ok {
		return "", 0, false
	}
	return e[0].(string), e[1].(uint64), true
}

func open(t *testing.T, dir string, b Backend, v Versions) *Workspace {
	t.Helper()
	w, err := Open(dir, b, v)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestGenerationAdvancesAndPersistsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	var last uint64
	for i := 1; i <= 5; i++ {
		w := open(t, dir, &fakeBackend{}, nil)
		if w.Generation() != uint64(i) || w.Generation() <= last {
			t.Fatalf("open %d: generation %d", i, w.Generation())
		}
		last = w.Generation()
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Workspace{closed: true}).Close(); !errors.Is(err, ErrClosed) {
		t.Fatal("double close")
	}
}

func TestSecondOpenIsBusyAndLeaksNoPath(t *testing.T) {
	dir := t.TempDir()
	w := open(t, dir, &fakeBackend{}, nil)
	_, err := Open(dir, &fakeBackend{}, nil)
	if !errors.Is(err, ErrBusy) || err.Error() != CodeBusy {
		t.Fatalf("second owner: %v", err)
	}
	if w.Generation() != 1 {
		t.Fatal("a refused open must not advance the generation")
	}
	// A symlink alias of the same directory cannot bypass the lock.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err = Open(alias, &fakeBackend{}, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("alias: %v", err)
	}
	w.Close()
	w2 := open(t, alias, &fakeBackend{}, nil)
	if w2.Generation() != 2 {
		t.Fatalf("after release: %d", w2.Generation())
	}
	w2.Close()
}

func TestOtherProcessCannotOpenWhileHeld(t *testing.T) {
	if dir := os.Getenv("TACET_WS_HELPER"); dir != "" {
		_, err := Open(dir, &fakeBackend{}, nil)
		if errors.Is(err, ErrBusy) {
			os.Exit(11)
		}
		if err != nil {
			os.Exit(12)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	run := func() int {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOtherProcessCannotOpenWhileHeld$")
		cmd.Env = append(os.Environ(), "TACET_WS_HELPER="+dir)
		err := cmd.Run()
		var exit *exec.ExitError
		if err == nil {
			return 0
		}
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		t.Fatal(err)
		return -1
	}
	w := open(t, dir, &fakeBackend{}, nil)
	if code := run(); code != 11 {
		t.Fatalf("held workspace opened by another process: exit %d", code)
	}
	w.Close()
	if code := run(); code != 0 {
		t.Fatalf("released workspace not openable: exit %d", code)
	}
}

func TestTornHeaderWriteKeepsTheHighestGeneration(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		open(t, dir, &fakeBackend{}, nil).Close()
	}
	path := filepath.Join(dir, headerName)
	data, _ := os.ReadFile(path)
	// Newest slot is the one with the larger sequence; tear it.
	_, idx, _, _ := func() (slot, int, bool, error) {
		f, _ := os.Open(path)
		defer f.Close()
		return readLatest(f)
	}()
	data[idx*slotSize+12] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	w := open(t, dir, &fakeBackend{}, nil)
	defer w.Close()
	// The torn slot is the one written during an Open that never returned a
	// session, so its generation was never handed to any worker; the surviving
	// slot (2) is the highest generation actually used and the next is 3.
	if w.Generation() != 3 {
		t.Fatalf("recovered generation %d, want 3", w.Generation())
	}
}

func TestCorruptOrFutureHeaderRefusesToOpen(t *testing.T) {
	dir := t.TempDir()
	open(t, dir, &fakeBackend{}, nil).Close()
	path := filepath.Join(dir, headerName)
	good, _ := os.ReadFile(path)

	garbage := make([]byte, headerSize)
	for i := range garbage {
		garbage[i] = 0xAB
	}
	os.WriteFile(path, garbage, 0o600)
	if _, err := Open(dir, &fakeBackend{}, nil); !errors.Is(err, ErrHeaderCorrupt) {
		t.Fatalf("garbage header: %v", err)
	}
	// A valid slot of a newer format must be left alone (AR-18).
	future := make([]byte, headerSize)
	copy(future, encode(slot{format: HeaderFormat + 1, generation: 9, sequence: 9}))
	os.WriteFile(path, future, 0o600)
	if _, err := Open(dir, &fakeBackend{}, nil); !errors.Is(err, ErrHeaderVersion) {
		t.Fatalf("future header: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(future) {
		t.Fatal("future-format header was modified")
	}
	// A refused open releases the lock.
	os.WriteFile(path, good, 0o600)
	if w, err := Open(dir, &fakeBackend{}, nil); err != nil {
		t.Fatalf("lock leaked by failed open: %v", err)
	} else {
		w.Close()
	}
	if _, err := Open(filepath.Join(dir, "missing"), &fakeBackend{}, nil); !errors.Is(err, ErrOpenFailed) {
		t.Fatal("missing directory")
	}
	if _, err := Open(dir, nil, nil); !errors.Is(err, ErrOpenFailed) {
		t.Fatal("nil backend")
	}
}

func TestCommitIsSerialAndRunsHooksOnlyAfterSuccess(t *testing.T) {
	b := &fakeBackend{}
	w := open(t, t.TempDir(), b, nil)
	defer w.Close()
	var counter int
	var ran atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Commit(context.Background(), func(tx store.Tx) error {
				counter++ // unsynchronised on purpose: -race proves serial execution
				tx.AfterCommit(func() { ran.Add(1) })
				tx.Emit(store.Event{Type: "x"})
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if counter != 50 || ran.Load() != 50 || b.maxActive.Load() != 1 {
		t.Fatalf("counter=%d hooks=%d max concurrent transactions=%d", counter, ran.Load(), b.maxActive.Load())
	}
}

func TestFailedOrPanickingCommitRollsBackWithoutHooks(t *testing.T) {
	b := &fakeBackend{}
	w := open(t, t.TempDir(), b, nil)
	defer w.Close()
	hook := false
	boom := errors.New("version conflict")
	if err := w.Commit(context.Background(), func(tx store.Tx) error {
		tx.AfterCommit(func() { hook = true })
		return boom
	}); !errors.Is(err, boom) || hook || b.rollbacks.Load() != 1 {
		t.Fatalf("fn error: %v hook=%v rollbacks=%d", err, hook, b.rollbacks.Load())
	}
	func() {
		defer func() { recover() }()
		w.Commit(context.Background(), func(tx store.Tx) error {
			tx.AfterCommit(func() { hook = true })
			panic("bug")
		})
	}()
	if hook || b.rollbacks.Load() != 2 {
		t.Fatalf("panic must roll back: hook=%v rollbacks=%d", hook, b.rollbacks.Load())
	}
	// The section is usable after a panic (the mutex was released).
	if err := w.Commit(context.Background(), func(store.Tx) error { return nil }); err != nil {
		t.Fatalf("section stuck after panic: %v", err)
	}
	b.failCommit = true
	if err := w.Commit(context.Background(), func(tx store.Tx) error {
		tx.AfterCommit(func() { hook = true })
		return nil
	}); err == nil || hook {
		t.Fatalf("a failed commit must not report success or run hooks: %v hook=%v", err, hook)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.failCommit = false
	if err := w.Commit(ctx, func(store.Tx) error { t.Fatal("ran with cancelled context"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAdmitRejectsLateResults(t *testing.T) {
	v := versions{"j1": {"a2", uint64(5)}}
	w := open(t, t.TempDir(), &fakeBackend{}, v)
	g := w.Generation()
	ok := kernel.AttemptRef{JobID: "j1", AttemptID: "a2", OwnerGeneration: g, JobVersion: 5}
	if err := w.Admit(ok); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]kernel.AttemptRef{
		"older generation":  {JobID: "j1", AttemptID: "a2", OwnerGeneration: g - 1, JobVersion: 5},
		"future generation": {JobID: "j1", AttemptID: "a2", OwnerGeneration: g + 1, JobVersion: 5},
		"abandoned attempt": {JobID: "j1", AttemptID: "a1", OwnerGeneration: g, JobVersion: 5},
		"older job version": {JobID: "j1", AttemptID: "a2", OwnerGeneration: g, JobVersion: 4},
		"newer job version": {JobID: "j1", AttemptID: "a2", OwnerGeneration: g, JobVersion: 6},
		"unknown job":       {JobID: "gone", AttemptID: "a2", OwnerGeneration: g, JobVersion: 5},
	} {
		if err := w.Admit(a); !errors.Is(err, ErrStale) {
			t.Errorf("%s admitted: %v", name, err)
		}
	}
	w.Close()
	if err := w.Admit(ok); !errors.Is(err, ErrClosed) {
		t.Errorf("closed workspace: %v", err)
	}
	// Without a version source nothing can be proven, so nothing is admitted.
	w2 := open(t, t.TempDir(), &fakeBackend{}, nil)
	defer w2.Close()
	if err := w2.Admit(kernel.AttemptRef{JobID: "j1", AttemptID: "a2", OwnerGeneration: w2.Generation(), JobVersion: 5}); !errors.Is(err, ErrStale) {
		t.Errorf("no version source: %v", err)
	}
	if err := w.Commit(context.Background(), func(store.Tx) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("commit after close: %v", err)
	}
}

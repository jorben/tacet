package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrBusy         = errors.New(CodeBusy)
	ErrClosed       = errors.New("WORKSPACE_CLOSED")
	ErrStale        = errors.New("WORKSPACE_STALE_RESULT")
	ErrOpenFailed   = errors.New("WORKSPACE_OPEN_FAILED")
	ErrNotAvailable = errors.New("WORKSPACE_LOCK_UNAVAILABLE")
)

// Txn is a backend transaction. Commit must run the hooks registered through
// AfterCommit only after the data is durable; Rollback discards them.
type Txn interface {
	store.Tx
	Commit() error
	Rollback() error
}

// Backend opens transactions on the workspace database. The store module
// supplies it; workspace owns ordering, not SQL.
type Backend interface {
	Begin(ctx context.Context) (Txn, error)
}

// BackendFunc adapts the store's concrete transaction factory without requiring
// store to import workspace (which would invert the dependency direction).
type BackendFunc func(context.Context) (Txn, error)

func (f BackendFunc) Begin(ctx context.Context) (Txn, error) { return f(ctx) }

// Versions answers what the current attempt and version of a job are, from
// the job table. ok=false means the job no longer exists.
type Versions interface {
	Current(tx store.Tx, jobID string) (attemptID string, version uint64, ok bool)
}

// Workspace is an open, exclusively owned workspace.
type Workspace struct {
	mu       sync.Mutex // the serial commit section
	header   *os.File
	gen      uint64
	backend  Backend
	versions Versions
	closed   bool
	keys     *keys.Manager
	closers  []func() error // released after the lock-holding commit section ends, in reverse order
}

var _ Session = (*Workspace)(nil)

const headerName = "workspace.header"

// Open resolves the workspace directory, takes the OS lock on the header file
// by kernel identity (not by lock-file existence), then reads the header and
// durably advances the execution generation. Nothing is returned before the
// new generation is synced, so every result of an earlier owner is stale.
func Open(dir string, backend Backend, versions Versions) (*Workspace, error) {
	if backend == nil {
		return nil, ErrOpenFailed
	}
	w, _, err := acquire(dir)
	if err != nil {
		return nil, err
	}
	w.backend, w.versions = backend, versions
	return w, nil
}

// acquire takes ownership: the OS lock and a durably advanced generation. It
// returns the resolved directory so callers place their files next to the header.
func acquire(dir string) (*Workspace, string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, "", ErrOpenFailed
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return nil, "", ErrOpenFailed
	}
	f, err := os.OpenFile(filepath.Join(real, headerName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, "", ErrOpenFailed
	}
	if err := platform.LockExclusive(f); err != nil {
		f.Close()
		if errors.Is(err, platform.ErrFileLockBusy) {
			return nil, "", ErrBusy
		}
		return nil, "", ErrNotAvailable
	}
	fail := func(err error) (*Workspace, string, error) {
		platform.UnlockExclusive(f)
		f.Close()
		return nil, "", err
	}
	cur, idx, found, err := readLatest(f)
	if err != nil {
		return fail(err)
	}
	next, err := writeNext(f, cur, idx, !found)
	if err != nil {
		return fail(ErrOpenFailed)
	}
	return &Workspace{header: f, gen: next.generation}, real, nil
}

func (w *Workspace) Generation() uint64 { return w.gen }

type hookTx struct {
	Txn
	owner *Workspace
	hooks []func()
}

func (h *hookTx) AfterCommit(fn func()) { h.hooks = append(h.hooks, fn) }

// Underlying exposes the same transaction to module-owned store repositories;
// it does not open another connection or transaction.
func (h *hookTx) Underlying() store.Tx { return h.Txn }

// Commit runs fn in the serial commit section: one at a time, in one
// transaction. fn must not call plugins or do bulk I/O. A returned error or a
// panic rolls back and no AfterCommit hook runs.
func (w *Workspace) Commit(ctx context.Context, fn func(tx store.Tx) error) error {
	hooks, err := w.commit(ctx, fn)
	if err != nil {
		return err
	}
	// Durable first, then release the serial section before dispatch. A hook
	// may submit work that performs another commit; holding mu here deadlocks.
	for _, hook := range hooks {
		hook()
	}
	return nil
}

func (w *Workspace) commit(ctx context.Context, fn func(store.Tx) error) ([]func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := w.backend.Begin(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := &hookTx{Txn: tx, owner: w}
	done := false
	defer func() {
		if !done {
			tx.Rollback()
		}
	}()
	if err = fn(wrapped); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	done = true
	return wrapped.hooks, nil
}

// Admit rejects results of an older generation, an abandoned attempt or an
// outdated job version. It reads the job table in its own short transaction
// inside the commit section, so it is ordered with decision changes.
func (w *Workspace) Admit(a kernel.AttemptRef) error {
	if a.OwnerGeneration != w.gen {
		return ErrStale
	}
	if w.versions == nil {
		return ErrStale // no way to check the attempt: fail closed
	}
	return w.Commit(context.Background(), func(tx store.Tx) error {
		return w.AdmitTx(tx, a)
	})
}

// AdmitTx is the final admission check inside Commit, using its transaction.
// A stage outcome must be checked and written in that same callback, otherwise
// a new decision/version could be committed between checking and writing.
// Do not call Admit from inside Commit: it opens a separate serial section.
func (w *Workspace) AdmitTx(tx store.Tx, a kernel.AttemptRef) error {
	owned, ok := tx.(*hookTx)
	if !ok || owned.owner != w || w.versions == nil || a.OwnerGeneration != w.gen {
		return ErrStale
	}
	attempt, version, ok := w.versions.Current(tx, a.JobID)
	if !ok || attempt != a.AttemptID || version != a.JobVersion {
		return ErrStale
	}
	return nil
}

// Close releases the lock. Pending commits finish first.
func (w *Workspace) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.closed = true
	var first error
	for i := len(w.closers) - 1; i >= 0; i-- { // database before keys before the lock
		if err := w.closers[i](); err != nil && first == nil {
			first = err
		}
	}
	platform.UnlockExclusive(w.header)
	if err := w.header.Close(); first == nil {
		first = err
	}
	return first
}

func closeHeader(w *Workspace) {
	platform.UnlockExclusive(w.header)
	w.header.Close()
}

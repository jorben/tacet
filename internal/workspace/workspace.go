package workspace

import (
	"context"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

// CodeBusy is returned when another process owns the workspace. Its message
// never contains the workspace path.
const CodeBusy = "WORKSPACE_BUSY"

// Session is an open workspace owned by this process.
type Session interface {
	// Generation is the execution generation taken when ownership was acquired.
	Generation() uint64
	// Commit runs fn in the serial commit section, in one transaction with
	// version checks. fn must not call plugins or do bulk I/O.
	Commit(ctx context.Context, fn func(tx store.Tx) error) error
	// Admit rejects late results from an older generation, attempt or job version.
	Admit(a kernel.AttemptRef) error
}

package platform

import "errors"

var ErrProcessStart = errors.New("PROCESS_START_FAILED")
var ErrProcessTree = errors.New("PROCESS_TREE_UNAVAILABLE")

// Tree is a started child plus the OS mechanism that lets the supervisor
// reclaim the child and its descendants (architecture 10, AR-05). It is not a
// sandbox; a descendant that deliberately leaves the group/job on a platform
// without a hard boundary is a documented limit, checked by monitoring.
type Tree struct {
	impl treeBackend
}

// treeBackend is implemented per platform by *treeImpl.
type treeBackend interface {
	pid() int
	kill() error
	release() error
}

// Pid is the direct child's process id (for diagnostics only).
func (t *Tree) Pid() int { return t.impl.pid() }

// Kill forcibly terminates the whole tree. It is idempotent.
func (t *Tree) Kill() error { return t.impl.kill() }

// Release frees OS handles after the tree has exited or been killed.
func (t *Tree) Release() error { return t.impl.release() }

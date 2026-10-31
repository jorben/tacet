//go:build darwin

package platform

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type treeImpl struct {
	cmd  *exec.Cmd
	once sync.Once
	err  error
}

func (t *treeImpl) pid() int { return t.cmd.Process.Pid }

// StartTree starts cmd as the leader of a new process group. Killing the
// negative pid reaches every member that stayed in the group. macOS has no
// hard process-tree boundary, so a descendant calling setsid escapes; the
// supervisor's memory/liveness monitoring and sandbox rules cover that case.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	if cmd == nil || cmd.Process != nil {
		return nil, ErrProcessStart
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	if err := cmd.Start(); err != nil {
		return nil, ErrProcessStart
	}
	return &Tree{impl: &treeImpl{cmd: cmd}}, nil
}

func (t *treeImpl) kill() error {
	t.once.Do(func() {
		// Pgid equals the leader pid because Setpgid with Pgid 0 was used.
		err := unix.Kill(-t.cmd.Process.Pid, unix.SIGKILL)
		if err != nil && !errors.Is(err, unix.ESRCH) {
			t.err = ErrProcessTree
		}
	})
	return t.err
}

func (t *treeImpl) release() error { return nil }

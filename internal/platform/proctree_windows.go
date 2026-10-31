//go:build windows

package platform

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type treeImpl struct {
	cmd  *exec.Cmd
	job  windows.Handle
	once sync.Once
	err  error
}

func (t *treeImpl) pid() int { return t.cmd.Process.Pid }

// StartTree starts cmd and assigns it to a Job Object configured to kill every
// member when the job closes. KNOWN LIMIT, to be verified on a real Windows
// host: the child runs briefly before assignment, so a child that spawns very
// early could escape; the production supervisor must start suspended or use a
// process-thread attribute list before admitting untrusted plugins.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	if cmd == nil || cmd.Process != nil {
		return nil, ErrProcessStart
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, ErrProcessTree
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, ErrProcessTree
	}
	if err = cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, ErrProcessStart
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		windows.CloseHandle(process)
	}
	if err != nil {
		cmd.Process.Kill()
		windows.CloseHandle(job)
		return nil, ErrProcessTree
	}
	return &Tree{impl: &treeImpl{cmd: cmd, job: job}}, nil
}

func (t *treeImpl) kill() error {
	t.once.Do(func() {
		if err := windows.TerminateJobObject(t.job, 1); err != nil {
			t.err = ErrProcessTree
		}
	})
	return t.err
}

func (t *treeImpl) release() error {
	if t.job != 0 {
		err := windows.CloseHandle(t.job)
		t.job = 0
		return err
	}
	return nil
}

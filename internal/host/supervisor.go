package host

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jorben/tacet/internal/platform"
)

var ErrSupervisorSpec = errors.New("SUPERVISOR_SPEC_INVALID")

// Spec describes one worker start. Path and Args are structured values, never a
// shell string. Env is the complete environment: nothing is inherited, so
// credentials and unrelated configuration cannot leak to a plugin (architecture
// 7). ExtraFiles are the only inherited handles (child fds 3, 4, ...).
type Spec struct {
	Path       string
	Args       []string
	Dir        string
	Env        []string
	ExtraFiles []*os.File
	// Generation, when set, tags the worker and every descendant that keeps its
	// environment, so Terminate (and a later startup scan of the previous
	// generation) also reclaims a descendant that escaped the process group.
	Generation string
}

// Worker is a started plugin process, its protocol Session and tree handle.
type Worker struct {
	Session    *Session
	tree       *platform.Tree
	cmd        *exec.Cmd
	done       chan struct{}
	exit       error
	generation string
	stderr     atomic.Int64
	once       sync.Once
}

type pipes struct {
	read  io.ReadCloser
	write io.WriteCloser
	once  sync.Once
}

func (p *pipes) Read(b []byte) (int, error)  { return p.read.Read(b) }
func (p *pipes) Write(b []byte) (int, error) { return p.write.Write(b) }
func (p *pipes) Close() error {
	p.once.Do(func() { p.read.Close(); p.write.Close() })
	return nil
}

// countingSink discards stderr: diagnostics may contain document values, so
// they are never stored or logged here. Only the volume is kept.
type countingSink struct{ n *atomic.Int64 }

func (c countingSink) Write(p []byte) (int, error) { c.n.Add(int64(len(p))); return len(p), nil }

// Start launches the worker in its own reclaimable tree and wraps stdio in a
// protocol Session. The caller still has to Initialize the session.
func Start(spec Spec) (*Worker, error) {
	if spec.Path == "" || !isAbsolute(spec.Path) {
		return nil, ErrSupervisorSpec
	}
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append([]string{}, spec.Env...)
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.ExtraFiles = spec.ExtraFiles
	if spec.Generation != "" {
		if err := platform.Mark(cmd, spec.Generation); err != nil {
			return nil, ErrSupervisorSpec
		}
	}
	// A descendant that escaped the group can keep the stderr pipe open after the
	// child exits; bound how long Wait waits for the copy instead of hanging.
	cmd.WaitDelay = time.Second
	w := &Worker{cmd: cmd, done: make(chan struct{}), generation: spec.Generation}
	cmd.Stderr = countingSink{&w.stderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, platform.ErrProcessStart
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, platform.ErrProcessStart
	}
	tree, err := platform.StartTree(cmd)
	if err != nil {
		return nil, err
	}
	w.tree = tree
	session, err := NewSession(&pipes{read: stdout, write: stdin})
	if err != nil {
		tree.Kill()
		cmd.Wait()
		tree.Release()
		return nil, err
	}
	w.Session = session
	go func() {
		w.exit = cmd.Wait()
		close(w.done)
	}()
	return w, nil
}

// Done is closed when the direct child has exited.
func (w *Worker) Done() <-chan struct{} { return w.done }

// StderrBytes is the volume of discarded diagnostics.
func (w *Worker) StderrBytes() int64 { return w.stderr.Load() }

// Terminate asks the worker to stop by closing its input, waits up to grace
// for a cooperative exit, and then reclaims the whole process tree. Either way
// no descendant is left running when it returns, and it can be called again.
func (w *Worker) Terminate(grace time.Duration) error {
	var result error
	w.once.Do(func() {
		w.Session.Close()
		select {
		case <-w.done:
			// The direct child left; descendants may remain.
		case <-time.After(grace):
		}
		result = w.tree.Kill()
		if w.generation != "" {
			// Descendants that left the group (setsid) are found by their marker.
			// This runs before waiting: such a descendant holds the stderr pipe
			// open, and cmd.Wait would otherwise wait for it.
			if _, err := platform.ReapMarked(w.generation); err != nil && result == nil {
				result = err
			}
		}
		<-w.done
		if err := w.tree.Release(); err != nil && result == nil {
			result = err
		}
	})
	return result
}

func isAbsolute(path string) bool {
	return len(path) > 0 && (path[0] == '/' || (len(path) > 2 && path[1] == ':'))
}

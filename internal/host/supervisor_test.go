//go:build darwin

package host

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/testenv"
)

func fixtureSpec(t *testing.T) Spec {
	t.Helper()
	python := testenv.Python(t)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "python", "tests", "worker_fixture.py"))
	return Spec{Path: python, Args: []string{"-I", fixture}, Env: []string{"PATH=/usr/bin:/bin"}}
}

func dead(pid int) bool {
	for i := 0; i < 300; i++ {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestSupervisorReclaimsUncooperativeWorkerAndDescendantAfterDeadline(t *testing.T) {
	worker, err := Start(fixtureSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	root := context.Background()
	if err = worker.Session.Initialize(root, "fixture.test", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(root, 700*time.Millisecond)
	defer cancel()
	grandchild := 0
	started := time.Now()
	_, err = worker.Session.Stream(ctx, "fixture.hang", kernel.AttemptRef{JobID: "j", AttemptID: "a", OwnerGeneration: 3, JobVersion: 1}, nil,
		func(_ context.Context, item json.RawMessage) error {
			var v struct {
				Pid int `json:"pid"`
			}
			json.Unmarshal(item, &v)
			grandchild = v.Pid
			return nil
		})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	if grandchild == 0 || syscall.Kill(grandchild, 0) != nil {
		t.Fatal("setup: descendant not running")
	}
	if err = worker.Terminate(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.Done():
	default:
		t.Fatal("worker still running after Terminate")
	}
	if !dead(grandchild) {
		t.Fatal("descendant survived tree reclaim")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("reclaim was not bounded")
	}
	if err = worker.Terminate(time.Second); err != nil {
		t.Fatalf("repeated Terminate: %v", err)
	}
}

func escapedPid(t *testing.T, worker *Worker) int {
	t.Helper()
	root := context.Background()
	if err := worker.Session.Initialize(root, "fixture.test", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	pid := 0
	_, err := worker.Session.Stream(root, "fixture.escape", kernel.AttemptRef{JobID: "j", AttemptID: "a", OwnerGeneration: 3, JobVersion: 1}, nil,
		func(_ context.Context, item json.RawMessage) error {
			var v struct {
				Pid int `json:"pid"`
			}
			json.Unmarshal(item, &v)
			pid = v.Pid
			return nil
		})
	if err != nil || pid == 0 || syscall.Kill(pid, 0) != nil {
		t.Fatalf("setup: escaped descendant not running (%v)", err)
	}
	return pid
}

func TestSupervisorReclaimsDescendantThatLeftTheProcessGroup(t *testing.T) {
	spec := fixtureSpec(t)
	spec.Generation = "gen_sup_" + time.Now().Format("150405.000000000")[7:] + "x"
	worker, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	pid := escapedPid(t, worker)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	if err = worker.Terminate(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !dead(pid) {
		t.Fatal("descendant that called setsid survived Terminate with a generation marker")
	}
}

func TestWithoutAGenerationMarkerASetsidDescendantSurvives(t *testing.T) {
	// documents the limit the marker closes; the process is cleaned up here
	worker, err := Start(fixtureSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	pid := escapedPid(t, worker)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	if err = worker.Terminate(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if syscall.Kill(pid, 0) != nil {
		t.Skip("the descendant did not escape the group on this system")
	}
}

func TestSupervisorRejectsAnInvalidGeneration(t *testing.T) {
	spec := fixtureSpec(t)
	spec.Generation = "bad marker"
	if _, err := Start(spec); !errors.Is(err, ErrSupervisorSpec) {
		t.Fatalf("got %v", err)
	}
}

func TestSupervisorDoesNotInheritEnvironmentOrAcceptRelativePaths(t *testing.T) {
	t.Setenv("TACET_TEST_SECRET", "must-not-reach-plugin")
	spec := fixtureSpec(t)
	spec.Args = []string{"-I", "-c", "import os,sys; sys.exit(7 if 'TACET_TEST_SECRET' in os.environ else 0)"}
	worker, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("child did not finish")
	}
	if worker.exit != nil {
		t.Fatalf("environment leaked or child failed: %v", worker.exit)
	}
	worker.Terminate(time.Second)
	if _, err = Start(Spec{Path: "python3"}); !errors.Is(err, ErrSupervisorSpec) {
		t.Fatalf("relative path accepted: %v", err)
	}
}

func TestSupervisorCrashEndsStreamWithoutHanging(t *testing.T) {
	spec := fixtureSpec(t)
	spec.Args = []string{"-I", "-c", "import os; os._exit(9)"}
	worker, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	if err = worker.Session.Initialize(ctx, "fixture.test", "0.1.0", 1); err == nil {
		t.Fatal("crashed worker completed handshake")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("crash detection waited for the deadline")
	}
}

// Signalled tells a worker that was killed from one that exited on its own (the reason code of a start
// that failed during the handshake depends on it); it reports nothing before the worker has ended.
func TestSignalledTellsAKilledWorkerFromOneThatExited(t *testing.T) {
	for _, c := range []struct {
		script string
		want   bool
	}{{"kill -9 $$", true}, {"exit 3", false}, {"exit 0", false}} {
		w, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", c.script}, Env: []string{"PATH=/usr/bin:/bin"}})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("did not end")
		}
		if got := w.Signalled(); got != c.want {
			t.Fatalf("%q: signalled=%v", c.script, got)
		}
		w.Terminate(100 * time.Millisecond)
	}
	w, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", "sleep 5"}, Env: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Terminate(100 * time.Millisecond)
	if w.Signalled() {
		t.Fatal("a running worker is not reported as signalled")
	}
}

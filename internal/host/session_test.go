package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/testenv"
)

type pipeTransport struct {
	read      io.ReadCloser
	write     io.WriteCloser
	closeOnce sync.Once
}

func (p *pipeTransport) Read(b []byte) (int, error)  { return p.read.Read(b) }
func (p *pipeTransport) Write(b []byte) (int, error) { return p.write.Write(b) }
func (p *pipeTransport) Close() error {
	p.closeOnce.Do(func() { p.read.Close(); p.write.Close() })
	return nil
}

func TestSessionHandshakeDeadlineUnblocksUnreadTransport(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	session, err := NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = session.Initialize(ctx, "fixture.test", "0.1.0", 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked write deadline: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("peer not reading prevented cancellation")
	}
}

func TestSessionRealPythonWorkerContract(t *testing.T) {
	python := testenv.Python(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "../../python/tests/worker_fixture.py")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	session, err := NewSession(&pipeTransport{read: stdout, write: stdin})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err = session.Initialize(ctx, "fixture.test", "0.1.0", 7); err != nil {
		t.Fatal(err)
	}
	attempt := kernel.AttemptRef{JobID: "j", AttemptID: "a", OwnerGeneration: 7, JobVersion: 1}
	seen := 0
	result, err := session.Stream(ctx, "fixture.stream", attempt, nil, func(ctx context.Context, item json.RawMessage) error {
		var value struct {
			Kind  string `json:"kind"`
			Index int    `json:"index"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			return err
		}
		if value.Kind != "fixture" || value.Index != seen {
			t.Fatal("unexpected item")
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 3 || result.LastSequence != 3 || result.Status != "completed" {
		t.Fatalf("stream outcome: %+v, seen=%d", result, seen)
	}
	attempt.AttemptID = "b"
	result, err = session.Stream(ctx, "fixture.explode", attempt, nil, func(context.Context, json.RawMessage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || result.Code != "PLUGIN_TASK_FAILED" {
		t.Fatalf("exception not redacted: %+v", result)
	}
}

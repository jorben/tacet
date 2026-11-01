//go:build darwin

package host

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
)

func shortDeadlines(t *testing.T) {
	d, g, w := defaultDeadlineMS, stallGrace, watchTick
	defaultDeadlineMS, stallGrace, watchTick = 300, 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { defaultDeadlineMS, stallGrace, watchTick = d, g, w })
}

// A plugin that ignores its deadline is stopped by the host after the deadline and a grace, even when the
// caller set none (the stages do not): the call ends as stalled and the process tree is reclaimed.
func TestAPluginThatIgnoresItsDeadlineIsStoppedByTheHost(t *testing.T) {
	shortDeadlines(t)
	worker, err := Start(fixtureSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx := context.Background()
	if err = worker.Session.Initialize(ctx, "fixture.test", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
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
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("expected the call to be stopped as stalled, got %v", err)
	}
	if took := time.Since(started); took < 600*time.Millisecond || took > 5*time.Second {
		t.Fatalf("stopped after %v: not before the deadline and the grace, and not much later", took)
	}
	if err = worker.Terminate(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if grandchild == 0 || !dead(grandchild) {
		t.Fatal("the process tree of the stalled plugin is reclaimed")
	}
}

// A plugin that keeps its deadline reports it itself, before the host would step in.
func TestAPluginThatKeepsItsDeadlineEndsTheCallItself(t *testing.T) {
	shortDeadlines(t)
	worker, err := Start(fixtureSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Terminate(time.Second)
	ctx := context.Background()
	if err = worker.Session.Initialize(ctx, "fixture.test", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	out, err := worker.Session.Stream(ctx, "fixture.cooperative", kernel.AttemptRef{JobID: "j", AttemptID: "a", OwnerGeneration: 3, JobVersion: 1}, nil,
		func(context.Context, json.RawMessage) error { return nil })
	if err != nil || out.Status != "failed" {
		t.Fatalf("%+v %v", out, err)
	}
	t.Logf("the plugin reported %s", out.Code)
}

// The budget of a stage is the deadline of its calls; a call the host had to stop counts as exceeding it,
// as does one the plugin ended itself for the deadline.
func TestABudgetIsTheDeadlineOfTheCallsUnderIt(t *testing.T) {
	shortDeadlines(t)
	defaultDeadlineMS = 60000 // the budget, not the default, must apply
	for _, method := range []string{"fixture.hang", "fixture.cooperative"} {
		worker, err := Start(fixtureSpec(t))
		if err != nil {
			t.Fatal(err)
		}
		defer worker.Terminate(100 * time.Millisecond) // also when the test fails: the hang leaves a descendant
		if err = worker.Session.Initialize(context.Background(), "fixture.test", "0.1.0", 3); err != nil {
			t.Fatal(err)
		}
		ctx, exceeded := WithBudget(context.Background(), 200*time.Millisecond)
		if exceeded() {
			t.Fatal()
		}
		started := time.Now()
		worker.Session.Stream(ctx, method, kernel.AttemptRef{JobID: "j", AttemptID: "a", OwnerGeneration: 3, JobVersion: 1}, nil,
			func(context.Context, json.RawMessage) error { return nil })
		if took := time.Since(started); took > 10*time.Second || !exceeded() {
			t.Fatalf("%s: took %v exceeded=%v", method, took, exceeded())
		}
	}
}

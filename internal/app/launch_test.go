package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jorben/tacet/internal/sched"
)

func TestAWorkerThatCouldNotStartSaysHowAndAStoppedStageIsNotAFailure(t *testing.T) {
	live := context.Background()
	if o := launchFailed(live, fmt.Errorf("%w: exec: no such file", errSpawn)); o.Kind != sched.Failed || o.Err.Code != "PLUGIN_START_FAILED" {
		t.Fatalf("%+v", o)
	}
	if o := launchFailed(live, fmt.Errorf("%w: timeout", errHandshake)); o.Kind != sched.Failed || o.Err.Code != "PLUGIN_HANDSHAKE_FAILED" {
		t.Fatalf("%+v", o)
	}
	for err, code := range map[error]string{errKilledEarly: "PLUGIN_KILLED_EARLY", errExitedEarly: "PLUGIN_EXITED_EARLY", errHandshakeTimeout: "PLUGIN_HANDSHAKE_TIMEOUT"} {
		if o := launchFailed(live, fmt.Errorf("%w: x", err)); o.Kind != sched.Failed || o.Err.Code != code {
			t.Fatalf("%v: %+v", err, o)
		}
	}
	if o := launchFailed(live, errors.New("anything else")); o.Err.Code != "PLUGIN_START_FAILED" {
		t.Fatalf("%+v", o)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	// paused or cancelled while the worker was starting: stopped, not failed, whatever the start error was
	for _, err := range []error{errSpawn, errHandshake, context.Canceled} {
		if o := launchFailed(stopped, err); o.Kind != sched.Cancelled || o.Err != nil {
			t.Fatalf("%v: %+v", err, o)
		}
	}
}

//go:build darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An occasional failed start is traceable by its code (the worker's own diagnostics are discarded): a
// worker that ends during the handshake, one that does not answer in time, one that answers as another.
func TestAFailedHandshakeSaysWhichWayItFailed(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	real, ok := e.pluginFor("txt")
	if !ok {
		t.Fatal("no text plugin")
	}
	ctx := context.Background()
	gone := real
	gone.module = "tacet_no_such_module" // python ends at once
	if _, err := e.launch(ctx, gone, nil); launchFailed(ctx, err).Err.Code != "PLUGIN_EXITED_EARLY" {
		t.Fatalf("%v", err)
	}
	// ended by a signal while it starts (the system, a reclaim): told apart from exiting on its own
	killer := filepath.Join(t.TempDir(), "python")
	os.WriteFile(killer, []byte("#!/bin/sh\nkill -9 $$\n"), 0o700)
	python := e.opt.Python
	e.opt.Python = killer
	_, err := e.launch(ctx, real, nil)
	e.opt.Python = python
	if launchFailed(ctx, err).Err.Code != "PLUGIN_KILLED_EARLY" {
		t.Fatalf("%v", err)
	}
	other := real
	other.id.ID = "tacet.other"
	if _, err := e.launch(ctx, other, nil); launchFailed(ctx, err).Err.Code != "PLUGIN_HANDSHAKE_FAILED" {
		t.Fatalf("%v", err)
	}
	old := handshakeTimeout
	handshakeTimeout = time.Millisecond
	_, err = e.launch(ctx, real, nil)
	handshakeTimeout = old
	if launchFailed(ctx, err).Err.Code != "PLUGIN_HANDSHAKE_TIMEOUT" {
		t.Fatalf("%v", err)
	}
	w, err := e.launch(ctx, real, nil)
	if err != nil {
		t.Fatalf("a normal start: %v", err)
	}
	w.Terminate(time.Second)
}

//go:build darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/snapshot"
)

// How the fake AstrLink worker of fakeModelPython starts.
const (
	workerWaits      = "wait"  // every start waits until release exists
	workerWaitsFirst = "first" // only the first start waits; later ones answer at once
	workerFails      = "fail"  // it exits before the handshake
)

// fakeModelPython is an interpreter for the engine: the AstrLink worker it starts appends its arguments to args,
// then (as mode says) waits until release exists and answers the handshake as the real plugin does on Core ML,
// without loading a model, or exits. Every other module runs on the real interpreter.
func fakeModelPython(t *testing.T, py, mode string) (python, args, release string) {
	t.Helper()
	dir := t.TempDir()
	python, args, release = filepath.Join(dir, "python"), filepath.Join(dir, "args"), filepath.Join(dir, "release")
	worker := "import sys\n" +
		"from tacet_plugin_sdk.runtime import Worker\n" +
		"def run(task, params):\n    raise RuntimeError('no model')\n" +
		"sys.exit(Worker(sys.stdin.buffer, sys.stdout.buffer, 'tacet.astrlink', '0.1.0', ['detect'], {'detect.run': run}, " +
		"capabilities={'backend.CoreMLExecutionProvider': True}).run())\n"
	script := "#!/bin/sh\n" +
		"if [ \"$3\" = tacet_detect_astrlink ]; then\n" +
		"  shift 3\n" +
		"  echo \"$@\" >> '" + args + "'\n" +
		"  mode='" + mode + "'\n" +
		"  if [ \"$mode\" = " + workerFails + " ]; then exit 1; fi\n" +
		"  if [ \"$mode\" = " + workerWaits + " ] || [ \"$(wc -l < '" + args + "' | tr -d ' ')\" -le 1 ]; then\n" +
		"    while [ ! -e '" + release + "' ]; do sleep 0.05; done\n" +
		"  fi\n" +
		"  exec '" + py + "' -I -c \"" + worker + "\"\n" +
		"fi\n" +
		"exec '" + py + "' \"$@\"\n"
	if err := os.WriteFile(python, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return python, args, release
}

func modelState(t *testing.T, e *Engine, id string) ModelRuntime {
	t.Helper()
	list, err := e.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("%s not listed: %+v", id, list)
	return ModelRuntime{}
}

const probeModel = "astrlink-guard-fp16"

// probeEngine opens a workspace with the FP16 AstrLink model preinstalled and run by a fake worker in mode; the
// start-up probe runs as in the desktop app. The engine is closed when the test ends, if the test did not.
func probeEngine(t *testing.T, mode string, probe bool) (e *Engine, args, release string) {
	t.Helper()
	r, py := root(t)
	python, args, release := fakeModelPython(t, py, mode)
	model := &ModelOption{Dir: t.TempDir(), ManifestPath: filepath.Join(r, "resources", "models", probeModel+".json"),
		Plugin: builtin(t, r, "detect-astrlink", "tacet_detect_astrlink")}
	e = openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, func(o *Options) {
		o.Python, o.Models, o.ProbeDevices = python, []*ModelOption{model}, probe
	})
	t.Cleanup(func() {
		e.Close() // ErrClosed when the test closed it
		os.WriteFile(release, nil, 0o600)
	})
	return e, args, release
}

func starts(t *testing.T, args string) []string {
	t.Helper()
	raw, err := os.ReadFile(args)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// eventually polls cond until it holds; the test's own timeout bounds it.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("still not %s after a minute", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// probeIsLoading waits until the probe holds the model slot and its worker has started (and is waiting).
func probeIsLoading(t *testing.T, e *Engine, args string) {
	t.Helper()
	eventually(t, "loading", func() bool {
		e.mu.Lock()
		holds := e.probeHolds
		e.mu.Unlock()
		return holds && len(starts(t, args)) == 1
	})
}

func probeEnded(e *Engine) bool {
	select {
	case <-e.probeDone:
		return true
	default:
		return false
	}
}

// A model installed in an earlier run shows the device it actually runs on, not "GPU first": at start-up the engine
// starts its worker under the auto policy and keeps what the handshake reports, before any task and without a trial.
// With GPU acceleration off nothing is started: the CPU needs no confirmation.
func TestTheDeviceOfAnInstalledModelIsConfirmedAtStartUp(t *testing.T) {
	r, py := root(t)
	python, args, release := fakeModelPython(t, py, workerWaits)
	model := &ModelOption{Dir: t.TempDir(), ManifestPath: filepath.Join(r, "resources", "models", "astrlink-guard-fp16.json"),
		Plugin: builtin(t, r, "detect-astrlink", "tacet_detect_astrlink")}
	ws := filepath.Join(t.TempDir(), "ws")
	opts := func(o *Options) { o.Python, o.Models, o.ProbeDevices = python, []*ModelOption{model}, true }
	id := "astrlink-guard-fp16"
	key := &memKey{}

	e := openEngineWith(t, ws, key, true, opts)
	// the worker has not answered yet: not confirmed, and the card is told it is being detected
	if rt := modelState(t, e, id); rt.Device != "auto" || !rt.Detecting {
		e.Close()
		t.Fatalf("before the handshake: device %q detecting %v, want auto and detecting", rt.Device, rt.Detecting)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	rt := modelState(t, e, id)
	for rt.Detecting && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		rt = modelState(t, e, id)
	}
	if rt.Device != "gpu" || rt.Backend != "CoreMLExecutionProvider" || !rt.Accelerated || rt.Detecting || rt.DeviceCode != "" {
		e.Close()
		t.Fatalf("after start-up the device is not confirmed: device %q backend %q accelerated %v detecting %v code %q", rt.Device, rt.Backend, rt.Accelerated, rt.Detecting, rt.DeviceCode)
	}
	raw, _ := os.ReadFile(args)
	if !strings.Contains(string(raw), "--backend auto") || strings.Count(string(raw), "\n") != 1 {
		e.Close()
		t.Fatalf("probe arguments: %q", raw)
	}
	setGPU(t, e, false)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	os.Remove(args)
	e = openEngineWith(t, ws, key, false, opts)
	rt = modelState(t, e, id)
	if err := e.Close(); err != nil { // waits for a probe, had one started
		t.Fatal(err)
	}
	if rt.Device != "cpu" || rt.Detecting {
		t.Fatalf("GPU off: device %q detecting %v", rt.Device, rt.Detecting)
	}
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatal("a worker was started to confirm the CPU")
	}
}

// The command line never shows the device, so an engine opened without ProbeDevices starts no worker for it.
func TestTheCommandLineStartsNoDeviceProbe(t *testing.T) {
	e, args, _ := probeEngine(t, workerWaits, false)
	if rt := modelState(t, e, probeModel); rt.Device != "auto" || rt.Detecting {
		t.Fatalf("device %q detecting %v, want auto, not detecting", rt.Device, rt.Detecting)
	}
	if e.probeDone != nil {
		t.Fatal("a probe was started")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if s := starts(t, args); len(s) != 0 {
		t.Fatalf("workers started: %q", s)
	}
}

// Closing while the probe's worker is still loading ends the probe and its worker before the workers are reclaimed,
// in either exit; nothing is started afterwards and nothing is left running.
func TestClosingDuringTheDeviceProbeEndsItsWorker(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(*Engine) error
	}{{"normal", (*Engine).Close}, {"system shutdown", (*Engine).CloseForSystem}} {
		t.Run(tc.name, func(t *testing.T) {
			e, args, _ := probeEngine(t, workerWaits, true)
			probeIsLoading(t, e, args)
			began := time.Now()
			if err := tc.close(e); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(began); took > exitGrace+probeExitWait {
				t.Fatalf("closing took %v", took)
			}
			if !probeEnded(e) {
				t.Fatal("the probe outlived the close")
			}
			if s := starts(t, args); len(s) != 1 {
				t.Fatalf("workers started: %q", s)
			}
			if n, err := platform.ReapMarkedPrefix(e.markerPrefix()); err != nil || n != 0 {
				t.Fatalf("left running after the close: %d (%v)", n, err)
			}
		})
	}
}

// A probe stopped by the exit after it took the model slot, but before it started its worker, starts none, and the
// exit waits for it: no python is started after the workers are reclaimed.
func TestAProbeStoppedBeforeItsWorkerStartsStartsNone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(*Engine) error
	}{{"normal", (*Engine).Close}, {"system shutdown", (*Engine).CloseForSystem}} {
		t.Run(tc.name, func(t *testing.T) {
			e, args, _ := probeEngine(t, workerWaits, false)
			// started here, not by Open, so that the hook is in place first: it holds the probe until the exit stops it
			e.beforeProbeLaunch = func(ctx context.Context) { <-ctx.Done() }
			e.opt.ProbeDevices = true
			e.startProbe(e.life)
			eventually(t, "holding the slot", func() bool {
				e.mu.Lock()
				defer e.mu.Unlock()
				return e.probeHolds
			})
			if err := tc.close(e); err != nil {
				t.Fatal(err)
			}
			if !probeEnded(e) {
				t.Fatal("the probe outlived the close")
			}
			if s := starts(t, args); len(s) != 0 {
				t.Fatalf("workers started: %q", s)
			}
		})
	}
}

// A worker that does not start leaves the device unconfirmed with the reason, and the probe over: the card says it
// is confirmed by the first task and stops asking.
func TestAFailedDeviceProbeSaysWhyAndEnds(t *testing.T) {
	e, args, _ := probeEngine(t, workerFails, true)
	eventually(t, "ended", func() bool { return probeEnded(e) })
	rt := modelState(t, e, probeModel)
	if rt.Device != "auto" || rt.Detecting || (rt.DeviceCode != "PLUGIN_EXITED_EARLY" && rt.DeviceCode != "PLUGIN_HANDSHAKE_FAILED") {
		t.Fatalf("device %q detecting %v code %q, want auto, not detecting, a start failure", rt.Device, rt.Detecting, rt.DeviceCode)
	}
	if s := starts(t, args); len(s) != 1 {
		t.Fatalf("workers started: %q", s)
	}
}

// A task that needs the model while the probe's worker is still loading does not wait for it: the probe gives the slot
// up and ends, and the task's own handshake confirms the device.
func TestATaskDoesNotWaitForTheDeviceProbe(t *testing.T) {
	e, args, _ := probeEngine(t, workerWaitsFirst, true)
	probeIsLoading(t, e, args)
	m := e.modelByID(probeModel)
	// the probe's worker waits for release, which is never written: the runner comes only if the probe yields
	pm, release, err := e.modelRunner(context.Background(), kernel.AttemptRef{JobID: "probe_test", AttemptID: "probe_test_a1", OwnerGeneration: e.ws.Generation(), JobVersion: 1},
		probeModel, &snapshot.ModelExecution{ModelID: probeModel, Digest: m.digest, Backend: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if pm == nil {
		t.Fatal("no runner")
	}
	release()
	eventually(t, "ended", func() bool { return probeEnded(e) })
	if rt := modelState(t, e, probeModel); rt.Device != "gpu" || rt.Detecting || rt.DeviceCode != "" {
		t.Fatalf("device %q detecting %v code %q, want gpu confirmed by the task", rt.Device, rt.Detecting, rt.DeviceCode)
	}
	if s := starts(t, args); len(s) != 2 {
		t.Fatalf("workers started: %q", s)
	}
}

// Turning GPU acceleration off while the probe waits for the model slot ends it: no worker is started for it.
func TestTurningGPUOffEndsAWaitingDeviceProbe(t *testing.T) {
	e, args, _ := probeEngine(t, workerWaits, false)
	// the probe is started here, not by Open, so that the slot is taken before it asks for it
	e.modelSem <- struct{}{}
	e.opt.ProbeDevices = true
	e.startProbe(e.life)
	if rt := modelState(t, e, probeModel); !rt.Detecting {
		<-e.modelSem
		t.Fatal("the probe is not waiting to confirm the model")
	}
	setGPU(t, e, false)
	<-e.modelSem
	eventually(t, "ended", func() bool { return probeEnded(e) })
	if rt := modelState(t, e, probeModel); rt.Detecting {
		t.Fatal("still detecting")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if s := starts(t, args); len(s) != 0 {
		t.Fatalf("workers started: %q", s)
	}
}

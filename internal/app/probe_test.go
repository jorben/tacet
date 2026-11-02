//go:build darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeModelPython is an interpreter for the engine: the AstrLink worker it starts records its arguments, waits
// until release exists, then answers the handshake as the real plugin does on Core ML, without loading a model.
// Every other module runs on the real interpreter.
func fakeModelPython(t *testing.T, py string) (python, args, release string) {
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
		"  while [ ! -e '" + release + "' ]; do sleep 0.05; done\n" +
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

// A model installed in an earlier run shows the device it actually runs on, not "GPU first": at start-up the engine
// starts its worker under the auto policy and keeps what the handshake reports, before any task and without a trial.
// With GPU acceleration off nothing is started: the CPU needs no confirmation.
func TestTheDeviceOfAnInstalledModelIsConfirmedAtStartUp(t *testing.T) {
	r, py := root(t)
	python, args, release := fakeModelPython(t, py)
	model := &ModelOption{Dir: t.TempDir(), ManifestPath: filepath.Join(r, "resources", "models", "astrlink-guard-fp16.json"),
		Plugin: builtin(t, r, "detect-astrlink", "tacet_detect_astrlink")}
	ws := filepath.Join(t.TempDir(), "ws")
	opts := func(o *Options) { o.Python, o.Models = python, []*ModelOption{model} }
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
	if rt.Device != "gpu" || rt.Backend != "CoreMLExecutionProvider" || !rt.Accelerated || rt.Detecting {
		e.Close()
		t.Fatalf("after start-up the device is not confirmed: device %q backend %q accelerated %v detecting %v", rt.Device, rt.Backend, rt.Accelerated, rt.Detecting)
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

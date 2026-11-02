package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/snapshot"
)

var (
	ErrAccelUnavailable = errors.New("ACCEL_UNAVAILABLE")
	ErrModelBusy        = errors.New("MODEL_BUSY")
	ErrTrialStart       = errors.New("MODEL_TRIAL_START_FAILED")
	ErrTrial            = errors.New("MODEL_TRIAL_FAILED")
)

// The first GPU start may compile the model. The provider can fall back to CPU, but the build cannot change.
var accelStartTimeout = 5 * time.Minute

func (e *Engine) execution(s config.Settings, m *modelInfo, avail map[string]bool) *snapshot.ModelExecution {
	if m == nil || !avail[m.id] {
		return nil
	}
	backend := "CPUExecutionProvider"
	if s.GPUAcceleration && deviceOf(m.backend) == "gpu" {
		backend = "auto"
	}
	return &snapshot.ModelExecution{ModelID: m.id, Digest: m.digest, Backend: backend}
}

func (e *Engine) accelCache(v *variantInfo) string { return e.modelCache(v.digest) }

func (e *Engine) modelCache(digest string) string {
	return filepath.Join(e.opt.Dir, "cache", "onnx", strings.TrimPrefix(digest, "sha256:")[:16])
}

// A snapshot pins graph, revision and digest. Only an auto provider may fall back, always on that graph.
// Legacy snapshots can still identify an explicitly frozen accelerated build.
func (e *Engine) modelArgs(ctx context.Context, m *modelInfo, exec *snapshot.ModelExecution) ([]string, time.Duration, error) {
	dir, manifest, digest, backend := m.opt.Dir, m.opt.ManifestPath, m.digest, "CPUExecutionProvider"
	entry := m.entry
	if exec != nil {
		backend = exec.Backend
		if exec.ModelID != m.id || exec.Digest != m.digest {
			v := m.accel
			if v == nil || v.id != exec.ModelID || v.digest != exec.Digest || v.backend != exec.Backend {
				return nil, 0, ErrAccelUnavailable
			}
			dir, manifest, digest, entry = v.dir, v.manifest, v.digest, v.entry
		}
	}
	var err error
	if dir, err = e.buildDir(ctx, dir, entry); err != nil {
		return nil, 0, err
	}
	if backend != "auto" && backend != "CPUExecutionProvider" && backend != "CoreMLExecutionProvider" && backend != "DmlExecutionProvider" && backend != "CUDAExecutionProvider" && backend != "ROCMExecutionProvider" {
		return nil, 0, ErrAccelUnavailable
	}
	timeout := handshakeTimeout
	if backend != "CPUExecutionProvider" {
		timeout = accelStartTimeout
	}
	return []string{"--model-dir", dir, "--manifest", manifest, "--backend", backend, "--cache-dir", e.modelCache(digest)}, timeout, nil
}

// recordBackend uses the worker's handshake, never just the requested provider, for device display.
func (e *Engine) recordBackend(m *modelInfo, requested string, session *host.Session) {
	backend := backendOf(session)
	if backend == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.modelBackends == nil {
		e.modelBackends = map[string]string{}
	}
	e.modelBackends[m.digest+"/"+requested] = backend
}

// backendOf is the provider a worker's handshake reports; "" if it reports none.
func backendOf(session *host.Session) string {
	for _, backend := range []string{"CoreMLExecutionProvider", "DmlExecutionProvider", "CUDAExecutionProvider", "ROCMExecutionProvider", "CPUExecutionProvider"} {
		if session.Capability("backend." + backend) {
			return backend
		}
	}
	return ""
}

func (e *Engine) reportedBackend(m *modelInfo, gpu bool) string {
	backend, _, _ := e.deviceState(m, gpu)
	return backend
}

// deviceState reads in one critical section what Models shows of m's device: the provider (auto before it is
// confirmed), whether the start-up probe is confirming it, and why the probe could not.
func (e *Engine) deviceState(m *modelInfo, gpu bool) (backend string, detecting bool, code string) {
	if !gpu || deviceOf(m.backend) != "gpu" {
		return "CPUExecutionProvider", false, ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if backend := e.modelBackends[m.digest+"/auto"]; backend != "" {
		return backend, false, ""
	}
	return "auto", e.probing[m.id], e.probeFailed[m.id]
}

func deviceOf(backend string) string {
	switch backend {
	case "CPUExecutionProvider", "":
		return "cpu"
	case "auto":
		return "auto"
	default:
		return "gpu"
	}
}

// startProbe confirms, in the background, the provider each installed GPU-capable model gets under the current
// preference, so the device shown after a restart is a fact and not "GPU first". Each worker is ended right after
// its handshake: no trial, no document. With GPU acceleration off nothing is probed: the CPU needs no confirmation.
// Any work that needs the model slot ends the probe for good (takeModelSlot): the task's own handshake confirms its
// model, and a model left unconfirmed is confirmed by its first task.
func (e *Engine) startProbe(ctx context.Context) {
	if !e.opt.ProbeDevices {
		return
	}
	ctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	e.probeDone = done
	var targets []*modelInfo
	if s, err := e.SettingsGet(ctx); err == nil && s.GPUAcceleration {
		for _, m := range e.models {
			if deviceOf(m.backend) == "gpu" {
				targets = append(targets, m)
			}
		}
	}
	e.mu.Lock()
	e.probeStop = stop
	e.probing = map[string]bool{}
	for _, m := range targets {
		e.probing[m.id] = true
	}
	e.mu.Unlock()
	go func() {
		defer close(done)
		defer func() {
			stop()
			e.mu.Lock()
			clear(e.probing)
			e.mu.Unlock()
		}()
		for _, m := range targets {
			if ctx.Err() != nil {
				return
			}
			e.probe(ctx, m)
			e.mu.Lock()
			delete(e.probing, m.id) // skipped or settled
			e.mu.Unlock()
		}
	}()
}

// probe starts m's worker under the auto policy and records the provider its handshake reports. A model that is
// not installed, or whose worker does not start, stays unconfirmed with the reason; a run confirms it then.
func (e *Engine) probe(ctx context.Context, m *modelInfo) {
	confirmed := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.modelBackends[m.digest+"/auto"] != ""
	}
	settled := func(code string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.probing, m.id)
		if e.probeFailed == nil {
			e.probeFailed = map[string]string{}
		}
		e.probeFailed[m.id] = code
	}
	if confirmed() || !e.eligible(m) {
		return
	}
	args, timeout, err := e.modelArgs(ctx, m, &snapshot.ModelExecution{ModelID: m.id, Digest: m.digest, Backend: "auto"})
	if err != nil {
		if !errors.Is(err, ErrModelNotInstalled) { // not installed: nothing to confirm
			settled(err.Error())
		}
		return
	}
	select {
	case e.modelSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	e.mu.Lock()
	e.probeHolds = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.probeHolds = false
		e.mu.Unlock()
		<-e.modelSem
	}()
	if e.beforeProbeLaunch != nil {
		e.beforeProbeLaunch(ctx)
	}
	if ctx.Err() != nil || confirmed() { // stopped, or a run or a trial loaded it while the probe waited
		return
	}
	if s, err := e.SettingsGet(ctx); err != nil || !s.GPUAcceleration { // turned off while the probe waited
		return
	}
	w, err := e.launchWithin(ctx, m.plug, args, nil, timeout)
	if err != nil {
		if ctx.Err() == nil { // stopped is not failed
			settled(launchCode(err))
		}
		return
	}
	e.mu.Lock()
	if backend := backendOf(w.Session); backend != "" {
		if e.modelBackends == nil {
			e.modelBackends = map[string]string{}
		}
		e.modelBackends[m.digest+"/auto"] = backend
	}
	delete(e.probing, m.id)
	delete(e.probeFailed, m.id)
	e.mu.Unlock()
	w.Terminate(time.Second)
}

// launchCode is the reason code of a worker that did not start.
func launchCode(err error) string {
	for _, c := range []error{errHandshakeTimeout, errKilledEarly, errExitedEarly, errHandshake, errSpawn} {
		if errors.Is(err, c) {
			return c.Error()
		}
	}
	return "PLUGIN_START_FAILED"
}

// stopProbe ends the start-up probe for good (it may be called again). No model is shown as being detected
// afterwards.
func (e *Engine) stopProbe() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.probeStop != nil {
		e.probeStop()
	}
	clear(e.probing)
}

// takeModelSlot takes the one model slot for real work, ending the start-up probe first, and queues for it: a slot
// the probe held is released at once.
func (e *Engine) takeModelSlot(ctx context.Context) error {
	e.stopProbe()
	select {
	case e.modelSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// probeExitWait bounds how long an exit waits for the start-up probe. It is cancelled by then, and launchWithin
// ends within about 1.2s of a cancelled handshake; waiting for it before reaping means no python starts after the
// reap.
var probeExitWait = 3 * time.Second

// waitProbe waits, at most d, for the start-up probe to end.
func (e *Engine) waitProbe(d time.Duration) {
	if e.probeDone == nil {
		return
	}
	select {
	case <-e.probeDone:
	case <-time.After(d):
	}
}

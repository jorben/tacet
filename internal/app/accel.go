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
	for _, backend := range []string{"CoreMLExecutionProvider", "DmlExecutionProvider", "CUDAExecutionProvider", "ROCMExecutionProvider", "CPUExecutionProvider"} {
		if session.Capability("backend." + backend) {
			e.mu.Lock()
			if e.modelBackends == nil {
				e.modelBackends = map[string]string{}
			}
			e.modelBackends[m.digest+"/"+requested] = backend
			e.mu.Unlock()
			return
		}
	}
}

func (e *Engine) reportedBackend(m *modelInfo, gpu bool) string {
	if !gpu || deviceOf(m.backend) != "gpu" {
		return "CPUExecutionProvider"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if backend := e.modelBackends[m.digest+"/auto"]; backend != "" {
		return backend
	}
	return "auto"
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
// A preference changed later is confirmed by the next run or start (a setting change starts no worker).
func (e *Engine) startProbe(ctx context.Context) {
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
	e.probing = map[string]bool{}
	for _, m := range targets {
		e.probing[m.id] = true
	}
	e.mu.Unlock()
	go func() {
		defer close(done)
		for _, m := range targets {
			e.probe(ctx, m)
			e.mu.Lock()
			delete(e.probing, m.id)
			e.mu.Unlock()
		}
	}()
}

// probe starts m's worker under the auto policy and records the provider its handshake reports. A model that is
// not installed, or whose worker does not start, stays unconfirmed; a run reports it then.
func (e *Engine) probe(ctx context.Context, m *modelInfo) {
	confirmed := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.modelBackends[m.digest+"/auto"] != ""
	}
	if confirmed() {
		return
	}
	args, timeout, err := e.modelArgs(ctx, m, &snapshot.ModelExecution{ModelID: m.id, Digest: m.digest, Backend: "auto"})
	if err != nil {
		return
	}
	select {
	case e.modelSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-e.modelSem }()
	if confirmed() { // a run or a trial loaded it while the probe waited
		return
	}
	w, err := e.launchWithin(ctx, m.plug, args, nil, timeout)
	if err != nil {
		return
	}
	e.recordBackend(m, "auto", w.Session)
	w.Terminate(time.Second)
}

func (e *Engine) isProbing(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.probing[id]
}

// waitProbe waits for the start-up probe (its context is the engine's, already cancelled when closing).
func (e *Engine) waitProbe(ctx context.Context) {
	if e.probeDone == nil {
		return
	}
	select {
	case <-e.probeDone:
	case <-ctx.Done():
	}
}

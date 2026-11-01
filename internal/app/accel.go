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

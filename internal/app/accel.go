package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/snapshot"
)

// Optional GPU acceleration: the FP16 build of AstrLink run by Core ML on Apple Silicon. The INT8 build on
// the CPU stays the baseline. Two separate things: the GPU build is a catalog entry downloaded, verified and
// tried like any model (plugins page); "GPU acceleration" is only a setting that means "use the GPU build
// when it is there". A batch freezes the build it runs (DetectContent.Execution).

var (
	ErrAccelUnavailable = errors.New("ACCEL_UNAVAILABLE")        // a frozen accelerated build is not there
	ErrModelBusy        = errors.New("MODEL_BUSY")               // a model worker runs (documents are being processed)
	ErrTrialStart       = errors.New("MODEL_TRIAL_START_FAILED") // the build did not load (Core ML missing, compile failed, timeout)
	ErrTrial            = errors.New("MODEL_TRIAL_FAILED")       // the build loaded but its trial run found the wrong things
)

// accelStartTimeout bounds the start of an accelerated worker: the first start compiles the model for
// Core ML (about 16 s on an M4, docs/v1/backend/m0-decisions.md), slower machines need more.
var accelStartTimeout = 5 * time.Minute

// execution is the build a batch of model m started now freezes: the accelerated one when acceleration is
// on and its build is downloaded, otherwise nil (the profile's own build, on the CPU).
func (e *Engine) execution(s config.Settings, m *modelInfo, avail map[string]bool) *snapshot.ModelExecution {
	if !s.GPUAcceleration || m == nil || m.accel == nil || !avail[m.accel.id] {
		return nil
	}
	v := m.accel
	return &snapshot.ModelExecution{ModelID: v.id, Digest: v.digest, Backend: v.backend}
}

// accelCache is the folder of the compiled Core ML model, inside the private workspace and keyed by
// the manifest digest so a changed build never meets a stale compiled one.
func (e *Engine) accelCache(v *variantInfo) string {
	return filepath.Join(e.opt.Dir, "cache", "coreml", strings.TrimPrefix(v.digest, "sha256:")[:16])
}

// modelArgs are the launch arguments of m's detection plugin for the build a snapshot froze.
func (e *Engine) modelArgs(ctx context.Context, m *modelInfo, exec *snapshot.ModelExecution) ([]string, time.Duration, error) {
	if exec == nil {
		dir, err := e.buildDir(ctx, m.opt.Dir, m.entry)
		if err != nil {
			return nil, 0, err
		}
		return []string{"--model-dir", dir, "--manifest", m.opt.ManifestPath}, handshakeTimeout, nil
	}
	v := m.accel
	if v == nil || v.id != exec.ModelID || v.digest != exec.Digest || v.backend != exec.Backend {
		return nil, 0, ErrAccelUnavailable
	}
	dir, err := e.buildDir(ctx, v.dir, v.entry)
	if err != nil {
		return nil, 0, ErrAccelUnavailable
	}
	return []string{"--model-dir", dir, "--manifest", v.manifest, "--cache-dir", e.accelCache(v)}, accelStartTimeout, nil
}

// deviceOf tells whether an ONNX Runtime execution provider computes on the CPU or on a GPU.
func deviceOf(backend string) string {
	switch backend {
	case "CPUExecutionProvider", "":
		return "cpu"
	}
	return "gpu"
}

//go:build darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/snapshot"
)

// accelOption is the production FP16 model; CPU and GPU execute these same files.
func accelOption(t *testing.T) *ModelOption {
	t.Helper()
	for _, m := range m0Models(t) {
		if strings.HasSuffix(m.ManifestPath, "astrlink-guard-fp16.json") && m.Dir != "" {
			return m
		}
	}
	t.Skip("needs the FP16 model resource")
	return nil
}

func writeManifest(t *testing.T, base string, edit func(map[string]any)) string {
	t.Helper()
	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	var man map[string]any
	json.Unmarshal(raw, &man)
	edit(man)
	p := filepath.Join(t.TempDir(), "manifest.json")
	data, _ := json.Marshal(man)
	os.WriteFile(p, data, 0o600)
	return p
}

func TestReadVariantAcceptsOnlyAnotherBuildOfTheSameModel(t *testing.T) {
	r, _ := root(t)
	base, err := os.ReadFile(filepath.Join(r, "resources", "models", "astrlink-guard-int8.json"))
	if err != nil {
		t.Fatal(err)
	}
	fp16 := filepath.Join(r, "resources", "models", "astrlink-guard-fp16.json")
	v := readVariant(base, &ModelVariant{Dir: "/m", ManifestPath: fp16})
	if v == nil || v.id != "astrlink-guard-fp16" || v.backend != "CoreMLExecutionProvider" || !strings.HasPrefix(v.digest, "sha256:") || v.dir != "/m" {
		t.Fatalf("%+v", v)
	}
	for name, edit := range map[string]func(map[string]any){
		"other model":   func(m map[string]any) { m["id"] = "other" },
		"other version": func(m map[string]any) { m["version"] = "0.2.0" },
		"same build":    func(m map[string]any) { m["variant"] = "int8" },
		"cpu backend":   func(m map[string]any) { m["backend"] = "CPUExecutionProvider" },
		"other labels": func(m map[string]any) {
			m["files"].(map[string]any)["labels.json"].(map[string]any)["sha256"] = strings.Repeat("0", 64)
		},
	} {
		if v := readVariant(base, &ModelVariant{Dir: "/m", ManifestPath: writeManifest(t, fp16, edit)}); v != nil {
			t.Errorf("%s accepted: %+v", name, v)
		}
	}
	if readVariant(base, &ModelVariant{Dir: "/m", ManifestPath: "/nonexistent.json"}) != nil {
		t.Error("a missing manifest was accepted")
	}
}

func TestTrialPassesOnlyWithEveryExpectedValueAboveTheScore(t *testing.T) {
	k := modelKinds["astrlink-guard"]
	at := func(v string) (int, int) {
		i := strings.Index(k.trialText, v)
		start := len([]rune(k.trialText[:i]))
		return start, start + len([]rune(v))
	}
	var all []detect.ModelSpan
	for label, v := range k.trialWant {
		s, e := at(v)
		all = append(all, detect.ModelSpan{Label: label, Start: s, End: e, Score: 0.95})
	}
	if !trialPassed(k, all, 0.8) {
		t.Fatal("the expected result did not pass")
	}
	if trialPassed(k, all[1:], 0.8) {
		t.Fatal("a missing value passed")
	}
	low := append([]detect.ModelSpan(nil), all...)
	low[0].Score = 0.6
	if trialPassed(k, low, 0.8) {
		t.Fatal("a value below the score passed")
	}
	shifted := append([]detect.ModelSpan(nil), all...)
	shifted[0].Start++
	if trialPassed(k, shifted, 0.8) {
		t.Fatal("a span with the wrong offsets passed")
	}
	out := append([]detect.ModelSpan(nil), all...)
	out[0].End = 999
	if trialPassed(k, out, 0.8) {
		t.Fatal("a span past the text passed")
	}
}

// setGPU saves the GPU acceleration setting, as the settings page does, and returns the first model's state.
func setGPU(t *testing.T, e *Engine, on bool) ModelRuntime {
	t.Helper()
	ctx := context.Background()
	s, err := e.SettingsGet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.GPUAcceleration = on
	if _, err = e.SettingsSave(ctx, s); err != nil {
		t.Fatal(err)
	}
	list, err := e.Models(ctx)
	if err != nil || len(list) == 0 {
		t.Fatal(list, err)
	}
	return list[0]
}

func TestGPUAccelerationDefaultsOnAndNeverChangesTheModelBuild(t *testing.T) {
	m := accelOption(t)
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(m))
	defer e.Close()
	ctx := context.Background()
	if s, _ := e.SettingsGet(ctx); !s.GPUAcceleration {
		t.Fatal("GPU preference is off by default")
	}
	for _, on := range []bool{true, false, true} {
		rt := setGPU(t, e, on)
		if rt.ID != "astrlink-guard-fp16" || rt.Build != rt.ID || rt.Dir != m.Dir || rt.Accel != nil {
			t.Fatalf("build changed: %+v", rt)
		}
		x := e.execution(config.Settings{GPUAcceleration: on}, e.models[0], e.available(ctx))
		want := "CPUExecutionProvider"
		if on {
			want = "auto"
		}
		if x == nil || x.ModelID != rt.ID || x.Digest != e.models[0].digest || x.Backend != want {
			t.Fatalf("%+v", x)
		}
	}
}

func TestAFrozenBuildThatIsGoneIsNotReplaced(t *testing.T) {
	m := accelOption(t)
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(m))
	defer e.Close()
	x := &snapshot.ModelExecution{ModelID: e.models[0].id, Digest: "sha256:" + strings.Repeat("0", 64), Backend: "auto"}
	if _, _, err := e.modelArgs(context.Background(), e.models[0], x); !errors.Is(err, ErrAccelUnavailable) {
		t.Fatal(err)
	}
	x.Digest = e.models[0].digest
	for _, backend := range []string{"auto", "CPUExecutionProvider"} {
		x.Backend = backend
		args, _, err := e.modelArgs(context.Background(), e.models[0], x)
		if err != nil || !strings.Contains(strings.Join(args, " "), "--model-dir "+m.Dir+" --manifest "+m.ManifestPath+" --backend "+backend) {
			t.Fatal(args, err)
		}
	}
}

// Both new batches and resumed work keep their frozen weights while the device preference can differ.
func TestGPUAndCPUUseTheSameFrozenFP16BuildEndToEnd(t *testing.T) {
	m := accelOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(m))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var previous *snapshot.ModelExecution
	for _, on := range []bool{true, false} {
		setGPU(t, e, on)
		name := "cpu.txt"
		if on {
			name = "gpu.txt"
		}
		p := filepath.Join(in, name)
		os.WriteFile(p, []byte("请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。\n"), 0o600)
		st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
			t.Fatal(err)
		}
		// A later setting change must not change the policy already frozen for this run.
		setGPU(t, e, !on)
		st, err = e.Wait(ctx, st.ID)
		if err != nil {
			t.Fatal(err)
		}
		f := st.Files[0]
		if f.State != "completed" || f.Categories["person"] < 1 {
			t.Fatalf("%+v", f)
		}
		l, err := e.load(ctx, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		exec := l.snap.Content().Detect.Execution
		want := "CPUExecutionProvider"
		if on {
			want = "auto"
		}
		if exec == nil || exec.ModelID != "astrlink-guard-fp16" || exec.Digest != e.models[0].digest || exec.Backend != want {
			t.Fatalf("%+v", exec)
		}
		if previous != nil && (previous.ModelID != exec.ModelID || previous.Digest != exec.Digest) {
			t.Fatal("CPU fallback changed weights")
		}
		previous = exec
		backend := e.reportedBackend(e.models[0], on)
		if backend == "auto" || (on && backend != "CoreMLExecutionProvider") || (!on && backend != "CPUExecutionProvider") {
			t.Fatalf("worker did not report the actual backend: %s", backend)
		}
		raw, _ := os.ReadFile(filepath.Join(out, f.Output))
		if strings.Contains(string(raw), "张伟") || !strings.Contains(string(raw), "[姓名]") {
			t.Fatalf("not redacted: %q", raw)
		}
	}
}

func TestHubRepositoryKeepsOnlyAPlainModelID(t *testing.T) {
	for in, want := range map[string]string{
		"QuantumNous/astrlink-guard": "QuantumNous/astrlink-guard", "a/b.c_d-1": "a/b.c_d-1",
		"": "", "astrlink-guard": "", "a/b/c": "", "../x": "", "a/..": "", "a/b..c": "", "-a/b": "",
		"https://evil.example/x": "", "a/b?x=1": "", "a/b#x": "", "a /b": "", "a/b\n": "",
	} {
		if got := hubRepository(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

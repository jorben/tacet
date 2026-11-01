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

// accelOption is the fixed model with its accelerated build (both M0 resource folders); skipped otherwise.
func accelOption(t *testing.T) *ModelOption {
	t.Helper()
	m := modelOption(t)
	if m.Accel == nil || m.Accel.Dir == "" {
		t.Skip("needs the FP16 model resource")
	}
	return m
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

// GPU acceleration is off by default and is only a setting: turning it on downloads, compiles and tries nothing;
// without the GPU build downloaded the model keeps running on the CPU.
func TestGPUAccelerationIsOnlyASettingAndOffByDefault(t *testing.T) {
	m := *modelOption(t)
	r, _ := root(t)
	m.Accel = &ModelVariant{ManifestPath: filepath.Join(r, "resources", "models", "astrlink-guard-fp16.json")} // to be downloaded
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, func(o *Options) { o.Models, o.ModelsRoot = []*ModelOption{&m}, t.TempDir() })
	defer e.Close()
	ctx := context.Background()
	if s, _ := e.SettingsGet(ctx); s.GPUAcceleration {
		t.Fatal("on by default")
	}
	rt := setGPU(t, e, true)
	if s, _ := e.SettingsGet(ctx); !s.GPUAcceleration {
		t.Fatal("the setting was not kept")
	}
	if !rt.Accelerable || rt.Accelerated || rt.Device != "cpu" || rt.Build != "astrlink-guard-int8" || rt.Accel == nil ||
		rt.Accel.ID != "astrlink-guard-fp16" || rt.Accel.State != "pending" || rt.Accel.Variant != "FP16" || rt.Accel.Bytes <= 0 || rt.Accel.Dir != "" {
		t.Fatalf("%+v %+v", rt, rt.Accel)
	}
	if x := e.execution(config.Settings{GPUAcceleration: true}, e.models[0], e.available(ctx)); x != nil {
		t.Fatalf("a batch would freeze a build that is not there: %+v", x)
	}
	// a model without an accelerated build reports none
	m2 := *modelOption(t)
	m2.Accel = nil
	e2 := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(&m2))
	defer e2.Close()
	if rt := setGPU(t, e2, true); rt.Accelerable || rt.Accel != nil || rt.Accelerated {
		t.Fatalf("%+v", rt)
	}
}

func TestAFrozenBuildThatIsGoneIsNotReplaced(t *testing.T) {
	m := accelOption(t)
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(m))
	defer e.Close()
	v := e.models[0].accel
	if _, _, err := e.modelArgs(context.Background(), e.models[0], &snapshot.ModelExecution{ModelID: v.id, Digest: "sha256:" + strings.Repeat("0", 64), Backend: v.backend}); !errors.Is(err, ErrAccelUnavailable) {
		t.Fatal(err)
	}
	args, timeout, err := e.modelArgs(context.Background(), e.models[0], nil)
	if err != nil || timeout != handshakeTimeout || strings.Join(args, " ") != "--model-dir "+m.Dir+" --manifest "+m.ManifestPath {
		t.Fatal(args, err)
	}
}

// Turned on, the trial run compiles and checks the FP16 build; a batch started then freezes that build
// and runs it on Core ML; turned off, the next batch runs the INT8 build on the CPU again.
func TestAccelerationRunsTheFrozenAcceleratedBuildEndToEnd(t *testing.T) {
	m := accelOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(m))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := setGPU(t, e, true)
	if !rt.Accelerated || !rt.Accelerable || rt.Device != "gpu" || rt.Backend != "CoreMLExecutionProvider" || rt.Dir != m.Accel.Dir || rt.ID != "astrlink-guard-int8" || rt.Build != "astrlink-guard-fp16" {
		t.Fatalf("%+v", rt)
	}
	run := func(name string) (FileStatus, *snapshot.ModelExecution) {
		p := filepath.Join(in, name)
		os.WriteFile(p, []byte("请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。\n"), 0o600)
		st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
			t.Fatal(err)
		}
		if st, err = e.Wait(ctx, st.ID); err != nil {
			t.Fatal(err)
		}
		f := st.Files[0]
		l, err := e.load(ctx, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		return f, l.snap.Content().Detect.Execution
	}
	f, exec := run("gpu.txt")
	if f.State != "completed" || f.Categories["person"] < 1 {
		t.Fatalf("%+v", f)
	}
	if exec == nil || exec.ModelID != "astrlink-guard-fp16" || exec.Backend != "CoreMLExecutionProvider" || exec.Digest != e.models[0].accel.digest {
		t.Fatalf("frozen: %+v", exec)
	}
	raw, _ := os.ReadFile(filepath.Join(out, f.Output))
	if strings.Contains(string(raw), "张伟") || !strings.Contains(string(raw), "[姓名]") {
		t.Fatalf("not redacted: %q", raw)
	}
	if entries, _ := os.ReadDir(e.accelCache(e.models[0].accel)); len(entries) == 0 {
		t.Fatal("the compiled model is not cached")
	}
	if rt = setGPU(t, e, false); rt.Accelerated || rt.Device != "cpu" || rt.Dir != m.Dir {
		t.Fatalf("%+v", rt)
	}
	f, exec = run("cpu.txt")
	if f.State != "completed" || f.Categories["person"] < 1 || exec != nil {
		t.Fatalf("%+v %+v", f, exec)
	}
}

// The detection stage runs the build its snapshot froze: when that build's files cannot be verified the file
// fails; the stage never falls back to the CPU build and delivers.
func TestTheDetectionStageRunsTheFrozenBuildAndNeverFallsBack(t *testing.T) {
	m := accelOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(m))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	setGPU(t, e, true)
	broken := *e.models[0].accel
	broken.dir = t.TempDir() // same identity, files gone
	e.models[0].accel = &broken
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("请联系张伟，电话 13812345678。\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if st.Files[0].State == "completed" || st.ExitCode == 0 {
		t.Fatalf("fell back instead of failing: %+v", st.Files[0])
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("nothing may be delivered: %v", entries)
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

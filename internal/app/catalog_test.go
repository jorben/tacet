//go:build darwin

package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/netgate"
)

func TestSourcesPointAtThePinnedRevision(t *testing.T) {
	r, _ := root(t)
	at := func(manifest string) source {
		s, err := sourceOf(filepath.Join(r, "resources", "models", manifest))
		if err != nil {
			t.Fatal(manifest, err)
		}
		return s
	}
	for manifest, files := range map[string]map[string]string{
		"astrlink-guard-int8.json": {"model_int8.onnx": "QuantumNous/astrlink-guard/resolve/7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4/int8/model_int8.onnx"},
		"astrlink-guard-fp16.json": {"tokenizer.json": "QuantumNous/astrlink-guard/resolve/7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4/fp16/tokenizer.json"},
		"privacy-filter-int8.json": {
			"config.json":               "openai/privacy-filter/resolve/7ffa9a043d54d1be65afb281eddf0ffbe629385b/config.json",
			"model_quantized.onnx_data": "openai/privacy-filter/resolve/7ffa9a043d54d1be65afb281eddf0ffbe629385b/onnx/model_quantized.onnx_data",
		},
	} {
		s := at(manifest)
		for file, want := range files {
			if got, err := s.url("https://huggingface.co", file); err != nil || got != "https://huggingface.co/"+want {
				t.Errorf("%s %s: %s %v", manifest, file, got, err)
			}
		}
	}
	for _, path := range []string{"../x", "onnx//x", "./x", ""} {
		s := source{repository: "a/b", revision: strings.Repeat("0", 40), paths: map[string]string{"f": path}}
		if path == "" {
			s.dir = ".."
		}
		if _, err := s.url("https://huggingface.co", "f"); err == nil {
			t.Errorf("%q accepted", path)
		}
	}
}

// managed is a catalog model whose files are to be downloaded (no preinstalled folder, no accelerated build).
func managed(m *ModelOption) *ModelOption {
	x := *m
	x.Dir, x.Accel = "", nil
	return &x
}

// hubServer serves the files of dir as the hub does at /<repository>/resolve/<revision>/<folder>/<file>,
// with a strong entity tag and ranges; it counts the requests.
func hubServer(t *testing.T, prefix, dir string) (*httptest.Server, *ModelSource, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		name, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		sum := sha256.Sum256([]byte(name + info.ModTime().String()))
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
		http.ServeContent(w, r, name, info.ModTime(), f)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, &ModelSource{Base: srv.URL, Hosts: []string{"127.0.0.1"}, TLS: &tls.Config{RootCAs: pool}}, &hits
}

func waitModel(t *testing.T, e *Engine, id string, done func(ModelRuntime) bool) ModelRuntime {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		list, err := e.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range list {
			if m.ID == id && done(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %+v", id, list)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A model starts out not downloaded: the office preset waits for it. Downloaded on
// request from the pinned source, it is verified, tried and installed into the shared folder; then it runs,
// and another workspace of the same user finds it there without downloading it again.
func TestAModelIsDownloadedVerifiedTriedAndThenUsed(t *testing.T) {
	m0 := modelOption(t)
	m := managed(m0)
	_, src, hits := hubServer(t, "/QuantumNous/astrlink-guard/resolve/7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4/int8/", m0.Dir)
	shared := t.TempDir()
	opts := func(o *Options) { o.Models, o.ModelsRoot, o.ModelSource = []*ModelOption{m}, shared, src }
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, opts)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := "astrlink-guard-int8"
	if st := waitModel(t, e, id, func(ModelRuntime) bool { return true }); st.State != "pending" || st.Dir != "" || st.Bytes <= 0 {
		t.Fatalf("%+v", st)
	}
	if e.defaultPreset(ctx) != ModelPresetID {
		t.Fatal("the office preset must keep its model while waiting for download")
	}
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("请联系张伟，电话 13812345678。\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: ModelPresetID})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: ModelPresetID, Authorize: true}); !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "MODEL_NOT_INSTALLED") {
		t.Fatalf("a preset whose model is not downloaded started: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the network was used before the user asked")
	}
	if err = e.DownloadModel(ctx, id); err != nil {
		t.Fatal(err)
	}
	ready := waitModel(t, e, id, func(r ModelRuntime) bool { return r.State == "ready" || r.State == "error" })
	if ready.State != "ready" || ready.Downloaded != ready.Bytes || !strings.HasPrefix(ready.Dir, filepath.Join(shared, "installed")+string(filepath.Separator)) {
		t.Fatalf("%+v", ready)
	}
	if e.defaultPreset(ctx) != ModelPresetID {
		t.Fatal("the downloaded model is not the default")
	}
	// the batch that waited for the model now starts
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: ModelPresetID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	st2, err := e.Wait(ctx, st.ID)
	if err != nil || st2.Files[0].State != "completed" || st2.Files[0].Categories["person"] < 1 {
		t.Fatalf("%+v %v", st2, err)
	}
	if dir, err := e.ModelDirectory(ctx, id); err != nil || dir != ready.Dir {
		t.Fatal(dir, err)
	}
	// another workspace over the same models folder
	before := hits.Load()
	e2 := openEngineWith(t, filepath.Join(t.TempDir(), "ws2"), &memKey{}, true, opts)
	defer e2.Close()
	if got := waitModel(t, e2, id, func(ModelRuntime) bool { return true }); got.State != "installed" || got.Dir != ready.Dir {
		t.Fatalf("%+v", got)
	}
	if e2.defaultPreset(ctx) != ModelPresetID || hits.Load() != before {
		t.Fatal("the installed model was not found, or was downloaded again")
	}
}

// A download that delivers other bytes than the manifest lists installs nothing.
func TestADownloadThatFailsVerificationInstallsNothing(t *testing.T) {
	m0 := modelOption(t)
	bad := t.TempDir()
	es, _ := os.ReadDir(m0.Dir)
	for _, f := range es {
		data, _ := os.ReadFile(filepath.Join(m0.Dir, f.Name()))
		if f.Name() == "tokenizer_config.json" {
			data[0] ^= 1 // same size, other content
		}
		os.WriteFile(filepath.Join(bad, f.Name()), data, 0o600)
	}
	_, src, _ := hubServer(t, "/QuantumNous/astrlink-guard/resolve/7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4/int8/", bad)
	shared := t.TempDir()
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, func(o *Options) {
		o.Models, o.ModelsRoot, o.ModelSource = []*ModelOption{managed(m0)}, shared, src
	})
	defer e.Close()
	if err := e.DownloadModel(context.Background(), "astrlink-guard-int8"); err != nil {
		t.Fatal(err)
	}
	got := waitModel(t, e, "astrlink-guard-int8", func(r ModelRuntime) bool { return r.State == "error" || r.State == "ready" })
	if got.State != "error" || got.Code != "MODEL_VERIFY_FAILED" || got.Dir != "" {
		t.Fatalf("%+v", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(shared, "installed")); len(entries) != 0 {
		t.Fatalf("installed: %v", entries)
	}
}

func TestADownloadIsRefusedBelowTheModelsMemoryWhileProcessingAndWhenPreinstalled(t *testing.T) {
	var pf *ModelOption
	for _, m := range m0Models(t) {
		if strings.HasSuffix(m.ManifestPath, "privacy-filter-fp16.json") {
			pf = managed(m)
		}
	}
	if pf == nil {
		t.Fatal("privacy-filter is not in the catalog")
	}
	memory := uint64(8 << 30)
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, func(o *Options) {
		o.Models, o.ModelsRoot = []*ModelOption{pf}, t.TempDir()
		o.Memory = func() (uint64, error) { return memory, nil }
	})
	defer e.Close()
	ctx := context.Background()
	id := "privacy-filter-fp16"
	got := waitModel(t, e, id, func(ModelRuntime) bool { return true })
	if got.Eligible || got.MinMemory != 16<<30 || got.Name != "OpenAI Privacy Filter" || got.Languages != "英文为主" || got.Preset != "builtin-preset-privacy-filter" {
		t.Fatalf("%+v", got)
	}
	if err := e.DownloadModel(ctx, id); !errors.Is(err, ErrModelMemory) {
		t.Fatal(err)
	}
	memory = 32 << 30
	lease, err := e.gate.Acquire(netgate.Processing)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DownloadModel(ctx, id); !errors.Is(err, ErrModelBusy) {
		t.Fatal(err)
	}
	lease.Release()
	if got := waitModel(t, e, id, func(ModelRuntime) bool { return true }); !got.Eligible || got.State != "pending" {
		t.Fatalf("%+v", got)
	}
	e.Close()
	e = openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(modelOption(t)))
	if err := e.DownloadModel(ctx, "astrlink-guard-int8"); !errors.Is(err, ErrNotAllowed) {
		t.Fatal(err)
	}
}

// The Privacy Filter preset runs the real model through its plugin: it finds what the rules cannot (an
// English name) and the delivered file no longer holds it.
func TestPrivacyFilterPresetFindsAndRedactsEndToEnd(t *testing.T) {
	pf := pfOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(modelOption(t), pf))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("Contact Jane Doe at jane.doe@example.org about the contract.\n"), 0o600)
	const preset = "builtin-preset-privacy-filter"
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: preset})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: preset, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	f := st.Files[0]
	if f.State != "completed" || f.Categories["person"] < 1 {
		t.Fatalf("%+v", f)
	}
	l, err := e.load(ctx, f.ID)
	if err != nil || l.snap.Content().Detect.ModelID != "privacy-filter-int8" {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, f.Output))
	if strings.Contains(string(raw), "Jane Doe") || strings.Contains(string(raw), "jane.doe@example.org") {
		t.Fatalf("not redacted: %q", raw)
	}
}

// Production FP16 is downloaded once, then the GPU preference only changes its provider.
func TestFP16IsDownloadedOnceAndCPUUsesTheSameInstallation(t *testing.T) {
	m0 := accelOption(t)
	_, src, hits := hubServer(t, "/QuantumNous/astrlink-guard/resolve/7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4/fp16/", m0.Dir)
	m := managed(m0)
	shared := t.TempDir()
	e := openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, func(o *Options) {
		o.Models, o.ModelsRoot, o.ModelSource = []*ModelOption{m}, shared, src
	})
	defer e.Close()
	ctx := context.Background()
	if hits.Load() != 0 {
		t.Fatal("downloaded without being asked")
	}
	if err := e.DownloadModel(ctx, "astrlink-guard-fp16"); err != nil {
		t.Fatal(err)
	}
	got := waitModel(t, e, "astrlink-guard-fp16", func(r ModelRuntime) bool { return r.State == "ready" || r.State == "error" })
	if got.State != "ready" || got.Accel != nil || got.Build != got.ID || got.Dir == "" {
		t.Fatalf("%+v", got)
	}
	before := hits.Load()
	rt := setGPU(t, e, false)
	if rt.Dir != got.Dir || rt.Build != got.Build || rt.Device != "cpu" || hits.Load() != before {
		t.Fatalf("%+v", rt)
	}
	if dir, err := e.ModelDirectory(ctx, got.ID); err != nil || dir != got.Dir {
		t.Fatal(dir, err)
	}
}

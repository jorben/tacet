package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/config"
)

// The production catalog is identical on CPU-only and GPU-capable platforms.
func TestCatalogUsesGPUCompatibleWeightsOnEveryPlatform(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"darwin-arm64", "windows-amd64"} {
		o := Options{Platform: platform}
		locateModels(root, "", &o)
		if len(o.Models) != 2 {
			t.Fatalf("%s: %d models", platform, len(o.Models))
		}
		for _, m := range o.Models {
			_, man, entry, err := readManifest(m.ManifestPath)
			if err != nil || man.Variant != "fp16" || deviceOf(man.Backend) != "gpu" || entry.Total() == 0 || m.Accel != nil {
				t.Fatalf("%s: %+v %v", platform, man, err)
			}
			info := &modelInfo{id: man.ID + "-" + man.Variant, digest: entry.Digest, backend: man.Backend, opt: m, entry: entry}
			e := Engine{opt: Options{Dir: t.TempDir()}}
			// No GPU build download can be substituted when the primary build is missing.
			if e.execution(config.Settings{GPUAcceleration: true}, info, map[string]bool{}) != nil {
				t.Fatal("uninstalled model accepted")
			}
			info.opt.Dir = t.TempDir()
			for _, enabled := range []bool{true, false} {
				exec := e.execution(config.Settings{GPUAcceleration: enabled}, info, map[string]bool{info.id: true})
				args, _, err := e.modelArgs(context.Background(), info, exec)
				if err != nil || exec.ModelID != info.id || exec.Digest != info.digest || !strings.Contains(strings.Join(args, " "), "--manifest "+m.ManifestPath) {
					t.Fatalf("%s: %+v %v", platform, exec, err)
				}
				if (exec.Backend == "auto") != enabled {
					t.Fatalf("%+v", exec)
				}
			}
		}
	}
}

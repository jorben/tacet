//go:build darwin

package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPluginsListsEveryRegisteredBuiltinOnceWithItsFormats(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	defer e.Close()
	ps, err := e.Plugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, p := range ps {
		if !p.Builtin || !p.Enabled || p.Version == "" || len(p.Roles) == 0 {
			t.Fatalf("%+v", p)
		}
		if _, dup := got[p.ID]; dup {
			t.Fatalf("%s listed twice", p.ID)
		}
		got[p.ID] = p.Formats
	}
	if !reflect.DeepEqual(got["tacet.text"], []string{"txt"}) || !reflect.DeepEqual(got["tacet.docx"], []string{"docx"}) || len(got) != 6 {
		t.Fatalf("%v", got)
	}
	for i := 1; i < len(ps); i++ {
		if ps[i-1].ID >= ps[i].ID {
			t.Fatal("not in a fixed order")
		}
	}
}

func TestModelsReportDirectoryDeviceAndSource(t *testing.T) {
	e := openEngine(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true)
	list, err := e.Models(context.Background())
	e.Close()
	if err != nil || len(list) != 0 {
		t.Fatalf("without a catalog: %+v %v", list, err)
	}
	o := modelOption(t)
	e = openEngineWith(t, filepath.Join(t.TempDir(), "ws"), &memKey{}, true, withModel(o))
	defer e.Close()
	list, err = e.Models(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	m := list[0]
	if m.Dir != o.Dir || m.Backend != "CPUExecutionProvider" || m.Device != "cpu" || m.ID != "astrlink-guard-int8" || m.Repository != "QuantumNous/astrlink-guard" ||
		m.Accelerated || m.Accelerable != (o.Accel != nil) || m.State != "ready" || m.Name != "AstrLink Guard" || m.Variant != "INT8" || m.Preset != ModelPresetID || !m.Eligible {
		t.Fatalf("%+v", m)
	}
}

func TestDeviceOfTellsCPUFromGPUProviders(t *testing.T) {
	for backend, want := range map[string]string{"CPUExecutionProvider": "cpu", "": "cpu", "CUDAExecutionProvider": "gpu",
		"CoreMLExecutionProvider": "gpu", "DmlExecutionProvider": "gpu"} {
		if got := deviceOf(backend); got != want {
			t.Errorf("%q: %s, want %s", backend, got, want)
		}
	}
}

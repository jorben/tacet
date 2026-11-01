package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/store"
)

func TestGPUProfilesAcceptSupportedProvidersAndRejectUnknownOnes(t *testing.T) {
	db, r := validatedRepository(t)
	for _, backend := range []string{"CoreMLExecutionProvider", "DmlExecutionProvider", "CUDAExecutionProvider", "ROCMExecutionProvider", "CPUExecutionProvider", "unknown"} {
		err := commit(t, db, func(tx store.Tx) error {
			_, err := r.Create(tx, backend, ModelProfile, backend, "", encoded(t, ModelContent{ModelID: "fixed-fp16", ManifestDigest: "sha256:" + strings.Repeat("a", 64), Backend: backend, AdapterFloor: 0.5, Calibrated: 0.8, LabelMap: map[string]string{"private_person": "person"}, MemoryBytes: 1}))
			return err
		})
		if (err != nil) != (backend == "unknown") {
			t.Fatalf("%s: %v", backend, err)
		}
	}
}

func TestGPUDefaultPreservesExplicitFalseAcrossReloads(t *testing.T) {
	db, r := database(t)
	if err := commit(t, db, func(tx store.Tx) error {
		s, err := r.Settings(tx)
		if err != nil {
			return err
		}
		if !s.GPUAcceleration {
			t.Fatal("fresh settings must enable GPU")
		}
		s.GPUAcceleration = false
		return r.SaveSettings(tx, s)
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		s, err := r.Settings(tx)
		if s.GPUAcceleration {
			t.Fatal("explicit false was lost")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRefreshUpdatesOnlyBuiltinProfilesAndKeepsVersionStableWhenEqual(t *testing.T) {
	db, r := database(t)
	if err := commit(t, db, func(tx store.Tx) error {
		if _, err := r.SeedBuiltin(tx, "model", ModelProfile, "model", []byte(`{"model_id":"astrlink-guard-int8"}`)); err != nil {
			return err
		}
		next := []byte(`{"model_id":"astrlink-guard-fp16"}`)
		if err := r.RefreshBuiltin(tx, "model", next); err != nil {
			return err
		}
		if err := r.RefreshBuiltin(tx, "model", next); err != nil {
			return err
		}
		if _, err := r.Create(tx, "custom", ModelProfile, "custom", "", []byte(`{"model_id":"custom"}`)); err != nil {
			return err
		}
		if err := r.RefreshBuiltin(tx, "custom", next); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("user profile was refreshed: %v", err)
		}
		got, err := r.Get(tx, "model")
		if got.Version != 2 || string(got.Content) != string(next) {
			t.Fatalf("%+v", got)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

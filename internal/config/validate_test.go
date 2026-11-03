package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
)

func validatedRepository(t *testing.T) (*store.DB, *Repository) {
	t.Helper()
	db, _ := database(t)
	validator, err := NewValidator(rules.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	return db, NewRepository(validator.Validate)
}
func encoded(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func seedReferences(t *testing.T, db *store.DB, r *Repository) {
	t.Helper()
	if err := commit(t, db, func(tx store.Tx) error {
		for _, object := range []struct {
			id    string
			kind  Kind
			value any
		}{
			{"rules", RuleSet, RulesContent{Rules: []RuleContent{{ID: "phone", Category: "phone", Pattern: `1\d{10}`}}}},
			{"scheme", Scheme, SchemeContent{CategoryActions: map[string]string{"person": "delete"}, ImageDefault: "remove"}},
			{"model1", ModelProfile, ModelContent{ModelID: "astrlink", ManifestDigest: "sha256:" + strings.Repeat("a", 64), Backend: "CPUExecutionProvider", AdapterFloor: 0.5, Calibrated: 0.8, LabelMap: map[string]string{"private_person": "person"}, MemoryBytes: 100}},
			{"model2", ModelProfile, ModelContent{ModelID: "astrlink", ManifestDigest: "sha256:" + strings.Repeat("b", 64), Backend: "CPUExecutionProvider", AdapterFloor: 0.6, Calibrated: 0.9, LabelMap: map[string]string{"private_person": "person"}, MemoryBytes: 100}},
		} {
			if _, err := r.Create(tx, object.id, object.kind, object.id, "", encoded(t, object.value)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func preset() PresetContent {
	return PresetContent{RuleSetID: "rules", SchemeID: "scheme", ModelProfileID: "model1", Mode: "guided", MinConfidence: 0.8, MemoryBytes: 1000, DPI: 200, Style: "black", RetentionDays: 7}
}

func TestConfigSchemasRejectUnsupportedContentAndRulesCompileBeforeSave(t *testing.T) {
	db, r := validatedRepository(t)
	for name, value := range map[string]string{
		"script":            `{"rules":[{"id":"x","category":"person","pattern":"x","script":"eval()"}]}`,
		"lookahead":         `{"rules":[{"id":"x","category":"person","pattern":"x(?=y)"}]}`,
		"invalid-validator": `{"rules":[{"id":"x","category":"person","pattern":"x","validator":"custom"}]}`,
		"both-forms":        `{"rules":[{"id":"x","category":"person","pattern":"x","terms":["y"]}]}`,
	} {
		err := commit(t, db, func(tx store.Tx) error { _, err := r.Create(tx, name, RuleSet, name, "", []byte(value)); return err })
		if err == nil {
			t.Fatalf("invalid rules saved: %s", name)
		}
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "valid", RuleSet, "valid", "", encoded(t, RulesContent{Rules: []RuleContent{{ID: "phone", Category: "phone", Pattern: `1\d{10}`, Boundary: "digit"}}}))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPresetReferencesThresholdAndModelSwitchReset(t *testing.T) {
	db, r := validatedRepository(t)
	seedReferences(t, db, r)
	p := preset()
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "preset", Preset, "preset", "", encoded(t, p))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p.ModelProfileID = "model2"
	p.MinConfidence = 0.51 // old value is below new adapter floor
	if err := commit(t, db, func(tx store.Tx) error {
		object, err := r.Save(tx, "preset", 1, encoded(t, p), "preset", "")
		if err != nil {
			return err
		}
		var saved PresetContent
		if err = json.Unmarshal(object.Content, &saved); err != nil {
			return err
		}
		if saved.MinConfidence != 0.9 || !object.MinConfidenceReset || object.Version != 2 {
			t.Fatalf("model switch not persisted with calibrated default: %+v %+v", object, saved)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.MinConfidence = 0.59
	if err := commit(t, db, func(tx store.Tx) error { _, err := r.Save(tx, "preset", 2, encoded(t, p), "preset", ""); return err }); !errors.Is(err, ErrContent) {
		t.Fatal("threshold below adapter floor accepted without model switch")
	}
	p = preset()
	p.RuleSetID = "missing"
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "missing", Preset, "missing", "", encoded(t, p))
		return err
	}); !errors.Is(err, ErrMissing) {
		t.Fatal("missing reference accepted")
	}
	p = preset()
	p.RuleSetID = "scheme"
	p.SchemeID = "scheme"
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "wrong-kind", Preset, "wrong-kind", "", encoded(t, p))
		return err
	}); !errors.Is(err, ErrContent) {
		t.Fatal("reference kind confused")
	}
}

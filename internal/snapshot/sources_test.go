package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// A category the scheme handles that nothing of the run detects (no rule of its rule sets that is on, and
// no label of its model) is left out of the run and listed as out of scope, rather than refusing the start:
// the scheme keeps the setting, and it applies again once a source comes back.
func TestACategoryNothingDetectsIsLeftOutOfTheRunAsOutOfScope(t *testing.T) {
	ctx := context.Background()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "s.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := store.NewRegistry(config.Module(), Module())
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	validator, _ := config.NewValidator(rules.DefaultBudget())
	repo := config.NewRepository(validator.Validate)
	commit := func(fn func(store.Tx) error) {
		t.Helper()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = fn(tx); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	commit(func(tx store.Tx) error {
		for _, o := range []struct {
			id   string
			kind config.Kind
			v    any
		}{
			{"phones", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{{ID: "p", Category: "phone", Pattern: `1\d{10}`}}}},
			{"ids", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{{ID: "w", Category: "work_id", Pattern: `HZ\d{6}`, Disabled: true}}}},
			{"scheme", config.Scheme, config.SchemeContent{CategoryActions: map[string]string{"phone": "placeholder", "work_id": "mask", "person": "placeholder"},
				ImageDefault: "remove", HumanReview: []string{"person", "phone", "work_id"}}},
			{"model", config.ModelProfile, config.ModelContent{ModelID: "m", ManifestDigest: digest, Backend: "CPUExecutionProvider", AdapterFloor: 0.3, Calibrated: 0.5,
				LabelMap: map[string]string{"private_person": "person"}, MemoryBytes: 1}},
		} {
			b, _ := json.Marshal(o.v)
			if _, err := repo.Create(tx, o.id, o.kind, o.id, "", b); err != nil {
				return err
			}
		}
		for id, p := range map[string]config.PresetContent{
			"rules":      {RuleSetID: "phones", ExtraRuleSetIDs: []string{"ids"}, SchemeID: "scheme", ModelProfileID: "model", Mode: "guided", RulesOnly: true, MemoryBytes: 1000, DPI: 200, Style: "black"},
			"with-model": {RuleSetID: "phones", ExtraRuleSetIDs: []string{"ids"}, SchemeID: "scheme", ModelProfileID: "model", Mode: "guided", MinConfidence: 0.5, MemoryBytes: 1000, DPI: 200, Style: "black"},
		} {
			b, _ := json.Marshal(p)
			if _, err := repo.Create(tx, id, config.Preset, id, "", b); err != nil {
				return err
			}
		}
		return nil
	})
	bindings := Bindings{Runtime: RuntimeContent{Protocol: "1.0", Formats: map[string]PluginRef{"txt": {ID: "t", Digest: "d", Env: "e"}}},
		Environment: Env{Formats: []string{"txt"}, FormatActions: map[string]map[string]bool{"txt": {"placeholder": true, "mask": true}},
			Models: map[string]Model{"m": {Categories: []string{"person"}, AdapterFloor: 0.3, MemoryBytes: 1}}, Categories: map[string]bool{"person": true, "phone": true}},
		ModelDigests: map[string]string{"m": digest}}
	for preset, want := range map[string]struct{ categories, out []string }{
		// the rule for work ids is off; rules only leaves the model's persons out
		"rules":      {[]string{"phone"}, []string{"person", "work_id"}},
		"with-model": {[]string{"person", "phone"}, []string{"work_id"}},
	} {
		commit(func(tx store.Tx) error {
			c, report, err := ResolvePreset(tx, repo, preset, bindings)
			if err != nil {
				return err
			}
			if len(report.Blocking) != 0 || len(report.NeedsNarrowing) != 0 {
				t.Errorf("%s: a category without a source must not block the start: %+v", preset, report)
			}
			actions := map[string]string{}
			for _, category := range want.categories {
				actions[category] = map[string]string{"phone": "placeholder", "person": "placeholder"}[category]
			}
			if !reflect.DeepEqual(c.Detect.Categories, want.categories) || !reflect.DeepEqual(c.Detect.OutOfScope, want.out) ||
				!reflect.DeepEqual(c.Dispose.CategoryAction, actions) || !reflect.DeepEqual(c.Dispose.HumanReview, want.categories) {
				t.Errorf("%s: categories %v, out of scope %v, actions %v, review %v", preset, c.Detect.Categories, c.Detect.OutOfScope, c.Dispose.CategoryAction, c.Dispose.HumanReview)
			}
			// the stored scheme keeps the person's settings
			var stored config.SchemeContent
			if json.Unmarshal(c.Dispose.SchemeContent, &stored); len(stored.CategoryActions) != 3 {
				t.Errorf("%s: the frozen scheme content lost a setting: %v", preset, stored.CategoryActions)
			}
			return nil
		})
	}
}

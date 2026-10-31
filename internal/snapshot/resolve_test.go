package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestResolvedPresetUsesLatestReferencesButFrozenTaskKeepsContent(t *testing.T) {
	ctx := context.Background()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(config.Module(), Module())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	validator, err := config.NewValidator(rules.DefaultBudget())
	if err != nil {
		t.Fatal(err)
	}
	repo := config.NewRepository(validator.Validate)
	commit := func(fn func(store.Tx) error) error {
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	manifest := "sha256:" + strings.Repeat("a", 64)
	if err = commit(func(tx store.Tx) error {
		objects := []struct {
			id      string
			kind    config.Kind
			content any
		}{
			{"rules", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{{ID: "phone", Category: "phone", Pattern: `1\d{10}`, Boundary: "digit"}}}},
			{"scheme", config.Scheme, config.SchemeContent{CategoryActions: map[string]string{"phone": "delete", "person": "placeholder"}, ImageDefault: "remove"}},
			{"model", config.ModelProfile, config.ModelContent{ModelID: "astrlink", ManifestDigest: manifest, Backend: "CPUExecutionProvider", AdapterFloor: 0.5, Calibrated: 0.8, LabelMap: map[string]string{"phone": "phone", "private_person": "person"}, MemoryBytes: 100}},
			{"preset", config.Preset, config.PresetContent{RuleSetID: "rules", SchemeID: "scheme", ModelProfileID: "model", Mode: "guided", MinConfidence: 0.8, MemoryBytes: 1000, DPI: 200, Style: "black", RetentionDays: 7}},
		}
		for _, object := range objects {
			body, err := json.Marshal(object.content)
			if err != nil {
				return err
			}
			if _, err = repo.Create(tx, object.id, object.kind, object.id, "", body); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bindings := Bindings{Runtime: RuntimeContent{Protocol: "1.0", Formats: map[string]PluginRef{"docx": {ID: "tacet.docx", Digest: "plugin-v1", Env: "env-v1"}}}, ModelDigests: map[string]string{"astrlink": manifest}, Environment: Env{Models: map[string]Model{"astrlink": {Categories: []string{"person", "phone"}, AdapterFloor: 0.5, MemoryBytes: 100}}, Formats: []string{"docx"}, FormatActions: map[string]map[string]bool{"docx": {"delete": true, "placeholder": true}}}}
	var old *Snapshot
	if err = commit(func(tx store.Tx) error {
		resolved, report, err := ResolvePreset(tx, repo, "preset", bindings)
		if err != nil {
			return err
		}
		if !report.Startable() || len(report.NeedsNarrowing) != 0 {
			t.Fatalf("report=%+v", report)
		}
		old, err = Freeze(resolved)
		if err != nil {
			return err
		}
		return (Repository{}).Save(tx, "batch-old", 0, old)
	}); err != nil {
		t.Fatal(err)
	}
	// Prune the original rule history completely while an old task holds its
	// own resolved body. The preset reference itself remains unchanged at v1.
	for version := uint64(1); version <= 6; version++ {
		if err = commit(func(tx store.Tx) error {
			body, _ := json.Marshal(config.RulesContent{Rules: []config.RuleContent{{ID: "term", Category: "person", Terms: []string{"project" + string(rune('A'+version))}}}})
			_, err := repo.Save(tx, "rules", version, body, "rules", "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = commit(func(tx store.Tx) error {
		latest, report, err := ResolvePreset(tx, repo, "preset", bindings)
		if err != nil {
			return err
		}
		if !report.Startable() || latest.Detect.RuleSet.Version != 7 {
			t.Fatalf("new task used old rules: %+v", latest.Detect.RuleSet)
		}
		stored, err := (Repository{}).Load(tx, "batch-old", 1)
		if err != nil {
			return err
		}
		if stored.Digest("detect") != old.Digest("detect") || stored.Content().Detect.RuleSet.Version != 1 || !strings.Contains(string(stored.Content().Detect.RuleContent), "phone") {
			t.Fatal("history pruning changed frozen task")
		}
		history, err := repo.History(tx, "rules")
		if err != nil {
			return err
		}
		if history[len(history)-1].Number <= 1 {
			t.Fatal("fixture did not prune original history")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = commit(func(tx store.Tx) error {
		bad := bindings
		bad.ModelDigests = map[string]string{"astrlink": "different"}
		if _, _, err := ResolvePreset(tx, repo, "preset", bad); !errors.Is(err, ErrBinding) {
			t.Fatal("mismatched manifest accepted")
		}
		bad = bindings
		bad.Runtime = RuntimeContent{Protocol: "1.0"}
		if _, _, err := ResolvePreset(tx, repo, "preset", bad); !errors.Is(err, ErrBinding) {
			t.Fatal("missing runtime binding accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

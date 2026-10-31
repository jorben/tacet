package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestExtraRuleSetsAreMergedIntoOneFrozenRuleList(t *testing.T) {
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
	create := func(tx store.Tx, id string, kind config.Kind, v any) error {
		b, _ := json.Marshal(v)
		_, err := repo.Create(tx, id, kind, id, "", b)
		return err
	}
	rule := func(id, cat, pat string) config.RuleContent {
		return config.RuleContent{ID: id, Category: cat, Pattern: pat}
	}
	if err = commit(func(tx store.Tx) error {
		for _, step := range []func() error{
			func() error {
				return create(tx, "a", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{rule("p", "phone", `1\d{10}`)}})
			},
			func() error {
				return create(tx, "b", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{rule("w", "work_id", `HZ\d{6}`)}})
			},
			func() error {
				return create(tx, "dup", config.RuleSet, config.RulesContent{Rules: []config.RuleContent{rule("p", "phone", `2\d{10}`)}})
			},
			func() error {
				return create(tx, "scheme", config.Scheme, config.SchemeContent{CategoryActions: map[string]string{"phone": "placeholder", "work_id": "placeholder"}, ImageDefault: "remove"})
			},
		} {
			if err := step(); err != nil {
				return err
			}
		}
		base := config.PresetContent{RuleSetID: "a", SchemeID: "scheme", Mode: "guided", RulesOnly: true, MemoryBytes: 1000, DPI: 200, Style: "black"}
		one := base
		two := base
		two.ExtraRuleSetIDs = []string{"b"}
		clash := base
		clash.ExtraRuleSetIDs = []string{"dup"}
		self := base
		self.ExtraRuleSetIDs = []string{"a"}
		if err := create(tx, "one", config.Preset, one); err != nil {
			return err
		}
		if err := create(tx, "two", config.Preset, two); err != nil {
			return err
		}
		if err := create(tx, "clash", config.Preset, clash); err != nil {
			return err
		}
		if err := create(tx, "self", config.Preset, self); !errors.Is(err, config.ErrContent) {
			t.Fatalf("a rule set listed twice: %v", err)
		}
		if err := create(tx, "kind", config.Preset, func() config.PresetContent { p := base; p.ExtraRuleSetIDs = []string{"scheme"}; return p }()); !errors.Is(err, config.ErrContent) {
			t.Fatalf("an extra that is not a rule set: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bindings := Bindings{Runtime: RuntimeContent{Protocol: "1.0", Formats: map[string]PluginRef{"txt": {ID: "t", Digest: "d", Env: "e"}}},
		Environment: Env{Formats: []string{"txt"}, FormatActions: map[string]map[string]bool{"txt": {"placeholder": true}}}}
	if err = commit(func(tx store.Tx) error {
		one, _, err := ResolvePreset(tx, repo, "one", bindings)
		if err != nil {
			return err
		}
		two, report, err := ResolvePreset(tx, repo, "two", bindings)
		if err != nil {
			return err
		}
		var merged config.RulesContent
		if json.Unmarshal(two.Detect.RuleContent, &merged); len(merged.Rules) != 2 || merged.Rules[0].ID != "p" || merged.Rules[1].ID != "w" {
			t.Fatalf("merged rules %+v", merged)
		}
		if len(two.Detect.ExtraRuleSets) != 1 || two.Detect.ExtraRuleSets[0].ID != "b" || two.Detect.RuleSet.ID != "a" {
			t.Fatalf("references %+v", two.Detect)
		}
		if one.Detect.RuleSetDigest == two.Detect.RuleSetDigest {
			t.Fatal("adding a rule set must change the detection digest")
		}
		if len(report.Blocking) != 0 {
			t.Fatalf("both categories are covered by rules: %+v", report)
		}
		if _, _, err := ResolvePreset(tx, repo, "clash", bindings); !errors.Is(err, ErrBinding) {
			t.Fatalf("a rule id used by two sets: %v", err)
		}
		// editing the extra set changes what is detected, so it changes the digest too
		b, _ := json.Marshal(config.RulesContent{Rules: []config.RuleContent{rule("w", "work_id", `HZ\d{7}`)}})
		if _, err := repo.Save(tx, "b", 1, b, "b", ""); err != nil {
			return err
		}
		again, _, err := ResolvePreset(tx, repo, "two", bindings)
		if err != nil {
			return err
		}
		if again.Detect.RuleSetDigest == two.Detect.RuleSetDigest || again.Detect.ExtraRuleSets[0].Version != 2 {
			t.Fatalf("an edited extra set must show: %+v", again.Detect.ExtraRuleSets)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

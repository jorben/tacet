//go:build darwin

package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/store"
)

func TestOneOfficePresetWithAndWithoutModelResources(t *testing.T) {
	for _, model := range []bool{false, true} {
		t.Run(map[bool]string{false: "rules-runtime", true: "model-runtime"}[model], func(t *testing.T) {
			ws, key := filepath.Join(t.TempDir(), "ws"), &memKey{}
			var tune func(*Options)
			if model {
				tune = withModel(managed(modelOption(t)), managed(pfOption(t)))
			}
			e := openEngineWith(t, ws, key, true, tune)
			ctx := context.Background()
			check := func() {
				t.Helper()
				list, err := e.ConfigList(ctx, "preset")
				if err != nil || len(list) != 1 || list[0].Name != "办公外发" || !list[0].ReadOnly {
					t.Fatalf("%+v %v", list, err)
				}
				if e.defaultPreset(ctx) != list[0].ID {
					t.Fatalf("default: %s", e.defaultPreset(ctx))
				}
				var p config.PresetContent
				if err := json.Unmarshal([]byte(list[0].Content), &p); err != nil {
					t.Fatal(err)
				}
				if p.RulesOnly == model {
					t.Fatalf("model binding changed: %+v", p)
				}
			}
			check()
			// Older builds used a suffixed name. Opening again migrates it without making more choices.
			err := e.ws.Commit(ctx, func(tx store.Tx) error {
				o, err := e.cfg.Get(tx, e.defaultPreset(ctx))
				if err != nil {
					return err
				}
				return e.cfg.RefreshBuiltin(tx, o.ID, o.Content, "办公外发（规则 + 模型）")
			})
			if err != nil {
				t.Fatal(err)
			}
			e.Close()
			e = openEngineWith(t, ws, key, false, tune)
			defer e.Close()
			check()
			if model {
				// Historical references still resolve, and a user's copy remains independently visible.
				var copied config.Object
				err = e.ws.Commit(ctx, func(tx store.Tx) error {
					var err error
					copied, err = e.cfg.Copy(tx, PresetID, "我自己的规则方案")
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				list, err := e.ConfigList(ctx, "preset")
				if err != nil || len(list) != 2 {
					t.Fatalf("%+v %v", list, err)
				}
				found := false
				for _, o := range list {
					if o.ID == copied.ID && !o.ReadOnly {
						found = true
					}
				}
				if !found {
					t.Fatal("user copy was hidden")
				}
			}
		})
	}
}

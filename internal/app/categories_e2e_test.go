//go:build darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/config"
)

// A person makes a category of their own with a rule, and the scheme handles it like any other: no
// list of categories changes in code. Its placeholder is what the output writes, and it is frozen with
// the run.
func TestAUserCategoryIsCreatedUsedByARuleAndWrittenAsItsPlaceholder(t *testing.T) {
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngine(t, ws, &memKey{}, true)
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	std, err := e.Categories(ctx)
	if err != nil || len(std) != 13 || std[0].ID != "person" || std[len(std)-1].ID != "other" {
		t.Fatalf("%+v %v", std, err)
	}
	cat, err := e.ConfigCreate(ctx, "category", "项目代号", "", `{"placeholder":"[项目]"}`)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := e.Categories(ctx)
	last := list[len(list)-1]
	if len(list) != 14 || last.ID != cat.ID || last.Name != "项目代号" || last.Origin != "custom" || !last.Manual || last.Version != 1 {
		t.Fatalf("%+v", list)
	}
	// names stay unambiguous across the whole catalog; content is bounded
	if _, err = e.ConfigCreate(ctx, "category", "电话", "", `{"placeholder":"[电话]"}`); !errors.Is(err, config.ErrNameConflict) {
		t.Fatalf("a shipped category's name: %v", err)
	}
	if _, err = e.ConfigCreate(ctx, "category", "项目代号", "", `{"placeholder":"[x]"}`); !errors.Is(err, config.ErrNameConflict) {
		t.Fatalf("a user category's name twice: %v", err)
	}
	for _, bad := range []string{`{}`, `{"placeholder":""}`, `{"placeholder":"[x]","color":"red"}`, `{"placeholder":"[x]","manual":true}`} {
		if _, err = e.ConfigCreate(ctx, "category", "坏的", "", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", bad, err)
		}
	}

	rs, err := e.ConfigCreate(ctx, "ruleset", "项目", "", `{"rules":[{"id":"p","category":"`+cat.ID+`","terms":["北斗"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	sch, err := e.ConfigCreate(ctx, "scheme", "含项目", "", `{"category_actions":{"phone":"placeholder","`+cat.ID+`":"placeholder"},"image_default":"remove"}`)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := e.ConfigCreate(ctx, "preset", "项目外发", "", `{"ruleset_id":"`+RuleSetID+`","extra_ruleset_ids":["`+rs.ID+`"],"scheme_id":"`+sch.ID+`","mode":"guided","rules_only":true,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(in, "p.txt")
	os.WriteFile(p, []byte("北斗 由 13812345678 负责\n"), 0o600)
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: pre.ID})
	if err != nil {
		t.Fatal(err)
	}
	// renamed with a new placeholder after the run was frozen: the run keeps what it froze
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Preset: pre.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ConfigSave(ctx, cat.ID, 1, "代号", "", `{"placeholder":"[代号]"}`); err != nil {
		t.Fatal(err)
	}
	if st, err = e.Wait(ctx, st.ID); err != nil || st.Files[0].State != "completed" || st.Files[0].Categories[cat.ID] != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, st.Files[0].Output))
	if s := string(raw); s != "[项目] 由 [手机号] 负责\n" {
		t.Fatalf("output %q", s)
	}
	if list, _ = e.Categories(ctx); list[len(list)-1].Name != "代号" || list[len(list)-1].Placeholder != "[代号]" {
		t.Fatalf("renamed: %+v", list[len(list)-1])
	}

	// a category a rule reports or a scheme handles cannot go; once nothing uses it, it can
	if err = e.ConfigDelete(ctx, cat.ID, 2); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("in use: %v", err)
	}
	if _, err = e.ConfigSave(ctx, rs.ID, 1, "项目", "", `{"rules":[{"id":"p","category":"other","terms":["北斗"]}]}`); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, cat.ID, 2); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("still handled by the scheme: %v", err)
	}
	if _, err = e.ConfigSave(ctx, sch.ID, 1, "含项目", "", `{"category_actions":{"phone":"placeholder"},"image_default":"remove"}`); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, cat.ID, 2); err != nil {
		t.Fatal(err)
	}
	if list, _ = e.Categories(ctx); len(list) != 13 || strings.HasPrefix(list[len(list)-1].ID, "cfg_") {
		t.Fatalf("%+v", list)
	}
}

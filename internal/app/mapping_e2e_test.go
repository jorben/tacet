//go:build darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/config"
)

// A model's labels map onto the application's categories, and the mapping is the user's to change: the
// real AstrLink Guard reports a person, the user's mapping makes it a category of their own, and the run
// writes that category's placeholder. A scheme that does not handle what the mapping now produces cannot
// start, rather than dropping what the model finds.
func TestAModelMappingSendsALabelToAUserCategory(t *testing.T) {
	m := modelOption(t)
	ws, in, out := filepath.Join(t.TempDir(), "ws"), t.TempDir(), t.TempDir()
	e := openEngineWith(t, ws, &memKey{}, true, withModel(m))
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	model := e.models[0].id

	cat, err := e.ConfigCreate(ctx, "category", "联系人", "", `{"placeholder":"[联系人]"}`)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for l, c := range e.models[0].labels {
		labels[l] = c
	}
	labels["private_person"] = cat.ID
	delete(labels, "private_date") // a label left out is not used
	mapping := func(id string, lm map[string]string) string {
		b, _ := json.Marshal(config.MappingContent{ModelID: id, LabelMap: lm})
		return string(b)
	}
	for name, c := range map[string][2]string{
		"named after another model": {"other-model", mapping(model, labels)},
		"unknown model":             {"other-model", mapping("other-model", labels)},
		"unknown label":             {model, mapping(model, map[string]string{"NOPE": "person"})},
		"unknown category":          {model, mapping(model, map[string]string{"private_person": "cfg_missing"})},
	} {
		if _, err = e.ConfigCreate(ctx, "model_mapping", c[0], "", c[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	mp, err := e.ConfigCreate(ctx, "model_mapping", model, "", mapping(model, labels))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ConfigCopy(ctx, mp.ID, "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a second mapping of the model: %v", err)
	}

	p := filepath.Join(in, "m.txt")
	os.WriteFile(p, []byte("请联系张伟，电话 13812345678。\n"), 0o600)
	// the office preset handles persons, which nothing detects any more: it does not start
	st, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st.ID, Authorize: true}); !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "no_detection_source") {
		t.Fatalf("start with an uncovered category: %v", err)
	}
	if err = e.Cancel(ctx, st.ID); err != nil {
		t.Fatal(err)
	}

	// a scheme that handles the new category instead of persons runs, and writes its placeholder
	sch, err := e.ConfigCreate(ctx, "scheme", "联系人方案", "", `{"category_actions":{"phone":"placeholder","`+cat.ID+`":"placeholder"},"image_default":"remove"}`)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := e.ConfigCreate(ctx, "preset", "联系人外发", "", `{"ruleset_id":"`+RuleSetID+`","scheme_id":"`+sch.ID+`","model_profile_id":"`+ModelProfileID+`","mode":"guided","rules_only":false,"min_confidence":0.5,"memory_bytes":1073741824,"dpi":200,"style":"black","retention_days":0}`)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := e.Import(ctx, ImportRequest{Kind: "batch", Files: []string{p}, OutputDir: out, Preset: pre.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(ctx, StartRequest{Batch: st2.ID, Preset: pre.ID, Authorize: true}); err != nil {
		t.Fatal(err)
	}
	if st2, err = e.Wait(ctx, st2.ID); err != nil || st2.Files[0].State != "completed" || st2.Files[0].Categories[cat.ID] < 1 || st2.Files[0].Categories["person"] != 0 {
		t.Fatalf("%+v %v", st2, err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, st2.Files[0].Output))
	if s := string(raw); !strings.Contains(s, "[联系人]") || strings.Contains(s, "张伟") || strings.Contains(s, "13812345678") {
		t.Fatalf("output %q", s)
	}

	// the category a mapping uses cannot go; resetting the mapping (deleting it) brings the default back
	if err = e.ConfigDelete(ctx, cat.ID, 1); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("in use by the mapping: %v", err)
	}
	if err = e.ConfigDelete(ctx, mp.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, cat.ID, 1); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("still handled by a scheme: %v", err)
	}
	if err = e.ConfigDelete(ctx, pre.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, sch.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.ConfigDelete(ctx, cat.ID, 1); err != nil {
		t.Fatalf("nothing uses it any more: %v", err)
	}
}

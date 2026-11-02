package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/jorben/tacet/internal/categories"
	"github.com/jorben/tacet/internal/snapshot"
)

// The label maps live in the manifests: every shipped manifest maps its labels onto known categories,
// and both builds of a model map them the same way.
func TestShippedManifestsMapLabelsOntoTheCatalog(t *testing.T) {
	want := map[string][]string{
		"astrlink-guard": {"account", "address", "date", "email", "ip_address", "payment_card", "person", "phone", "secret", "url"},
		"privacy-filter": {"account", "address", "date", "email", "person", "phone", "secret", "url"},
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "resources", "models", "*.json"))
	if len(files) < 4 {
		t.Fatalf("manifests: %v", files)
	}
	for _, f := range files {
		_, man, _, err := readManifest(f)
		if err != nil {
			t.Fatal(f, err)
		}
		_, cats, err := modelCategories(man, modelKinds[man.ID])
		if err != nil {
			t.Fatal(f, err)
		}
		if !slices.Equal(cats, want[man.ID]) {
			t.Errorf("%s: categories %v", filepath.Base(f), cats)
		}
	}
}

// A new model needs a manifest, not code: labels may map to categories the manifest declares, and a
// label mapped to an undefined category is refused.
func TestModelManifestsMayDeclareCategoriesButNotMapToUnknownOnes(t *testing.T) {
	man := modelManifest{ID: "plates", LabelMap: map[string]string{"PLATE": "license_plate", "PER": "person"},
		Categories: []categories.Decl{{ID: "license_plate", Name: "车牌", Placeholder: "[车牌]"}}}
	declared, cats, err := modelCategories(man, modelKind{})
	if err != nil || len(declared) != 1 || declared[0].Origin != categories.Model || !slices.Equal(cats, []string{"license_plate", "person"}) {
		t.Fatalf("declared %+v cats %v err %v", declared, cats, err)
	}
	for name, m := range map[string]modelManifest{
		"unknown category": {LabelMap: map[string]string{"PLATE": "license_plate"}},
		"no label map":     {},
	} {
		if _, _, err := modelCategories(m, modelKind{}); err != ErrOptions {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, _, err := modelCategories(modelManifest{LabelMap: map[string]string{"PER": "person"}}, modelKind{trialWant: map[string]string{"PHONE": "1"}}); err != ErrOptions {
		t.Error("a trial label without mapping was accepted")
	}
}

func TestRendererUsesFrozenPlaceholdersAndTheStandardOnesForOlderSnapshots(t *testing.T) {
	var c snapshot.Content
	old := renderer(c)
	if old.Placeholders["phone"] != "[手机号]" || old.Placeholders["organization"] != "[机构]" || old.Fallback != "[已处理]" {
		t.Fatalf("older snapshot placeholders = %v", old.Placeholders)
	}
	c.Render.Placeholders = map[string]string{"cfg_x": "[项目]"}
	if r := renderer(c); r.Placeholders["cfg_x"] != "[项目]" || r.Placeholders["phone"] != "" {
		t.Fatalf("frozen placeholders = %v", r.Placeholders)
	}
}

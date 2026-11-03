package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jorben/tacet/internal/store"
)

// A rule set has at least one rule, a model mapping counts at least one label, and a preset that detects
// with a model names it: each of them detects something.
func TestEveryDetectionSourceDetectsSomething(t *testing.T) {
	db, r := validatedRepository(t)
	seedReferences(t, db, r)
	for name, c := range map[string]struct {
		kind    Kind
		content string
	}{
		"a rule set without rules":       {RuleSet, `{"rules":[]}`},
		"a mapping that counts no label": {ModelMapping, `{"model_id":"astrlink","label_map":{}}`},
		"model + rules without a model":  {Preset, `{"ruleset_id":"rules","scheme_id":"scheme","mode":"guided","rules_only":false,"memory_bytes":1,"dpi":200,"style":"black","retention_days":0}`},
	} {
		if err := commit(t, db, func(tx store.Tx) error {
			_, err := r.Create(tx, "x", c.kind, "x", "", []byte(c.content))
			return err
		}); !errors.Is(err, ErrContent) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]struct {
		kind    Kind
		content string
	}{
		"a rule set whose rules are all off": {RuleSet, `{"rules":[{"id":"a","category":"phone","terms":["x"],"disabled":true}]}`},
		"a mapping that counts one label":    {ModelMapping, `{"model_id":"astrlink","label_map":{"private_person":"person"}}`},
		"rules only without a model":         {Preset, `{"ruleset_id":"rules","scheme_id":"scheme","mode":"guided","rules_only":true,"memory_bytes":1,"dpi":200,"style":"black","retention_days":0}`},
	} {
		if err := commit(t, db, func(tx store.Tx) error {
			_, err := r.Create(tx, "ok-"+string(c.kind), c.kind, "ok-"+string(c.kind), "", []byte(c.content))
			return err
		}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A category a rule reports stays. One that only model mappings and schemes refer to goes once the person
// confirms: the labels that counted as it are no longer used, schemes no longer hold a setting for it, each
// as a new version; what else they hold is kept. A mapping that would be left counting no label keeps it.
func TestDeletingACategoryKeepsItForRulesAndTakesItOutOfMappingsAndSchemes(t *testing.T) {
	db, r := validatedRepository(t)
	seedReferences(t, db, r)
	get := func(id string) Object {
		t.Helper()
		var o Object
		if err := commit(t, db, func(tx store.Tx) (err error) { o, err = r.Get(tx, id); return err }); err != nil {
			t.Fatal(err)
		}
		return o
	}
	create := func(id string, kind Kind, content string) {
		t.Helper()
		if err := commit(t, db, func(tx store.Tx) error { _, err := r.Create(tx, id, kind, id, "", []byte(content)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	del := func(id string) error {
		version := get(id).Version
		return commit(t, db, func(tx store.Tx) error { return r.Delete(tx, id, version) })
	}
	create("cfg_a", Category, `{"placeholder":"[A]"}`)
	create("cfg_b", Category, `{"placeholder":"[B]"}`)
	create("mapping", ModelMapping, `{"model_id":"astrlink","label_map":{"private_person":"cfg_a","phone":"phone","email":"cfg_a"}}`)
	create("only", ModelMapping, `{"model_id":"other","label_map":{"private_person":"cfg_b"}}`)
	create("s1", Scheme, `{"category_actions":{"cfg_a":"mask","phone":"placeholder"},"image_default":"remove","human_review":["cfg_a","phone"]}`)
	create("s2", Scheme, `{"category_actions":{"phone":"placeholder"},"image_default":"remove"}`)
	create("rs", RuleSet, `{"rules":[{"id":"r","category":"cfg_a","terms":["x"],"disabled":true}]}`)

	// a rule reports it (switched off or not): it stays, and nothing else changes
	if err := del("cfg_a"); !errors.Is(err, ErrInUse) {
		t.Fatalf("reported by a rule: %v", err)
	}
	if get("mapping").Version != 1 || get("s1").Version != 1 {
		t.Fatal("a refused delete changed what refers to the category")
	}
	if _, err := commitSave(t, db, r, "rs", `{"rules":[{"id":"r","category":"phone","terms":["x"]}]}`); err != nil {
		t.Fatal(err)
	}
	if err := del("cfg_a"); err != nil {
		t.Fatal(err)
	}
	var m MappingContent
	json.Unmarshal(get("mapping").Content, &m)
	if get("mapping").Version != 2 || !reflect.DeepEqual(m.LabelMap, map[string]string{"phone": "phone"}) {
		t.Fatalf("mapping v%d %v", get("mapping").Version, m.LabelMap)
	}
	var s SchemeContent
	json.Unmarshal(get("s1").Content, &s)
	if get("s1").Version != 2 || !reflect.DeepEqual(s.CategoryActions, map[string]string{"phone": "placeholder"}) || !reflect.DeepEqual(s.HumanReview, []string{"phone"}) || s.ImageDefault != "remove" {
		t.Fatalf("scheme v%d %+v", get("s1").Version, s)
	}
	if get("s2").Version != 1 {
		t.Fatal("a scheme that does not refer to the category got a new version")
	}

	// the only label of a mapping counts as it: the category stays, the mapping is as it was
	if err := del("cfg_b"); !errors.Is(err, ErrInUse) {
		t.Fatalf("the only label of a mapping: %v", err)
	}
	if get("only").Version != 1 {
		t.Fatal("a refused delete changed the mapping")
	}
}

func commitSave(t *testing.T, db *store.DB, r *Repository, id, content string) (Object, error) {
	t.Helper()
	var out Object
	err := commit(t, db, func(tx store.Tx) error {
		o, err := r.Get(tx, id)
		if err != nil {
			return err
		}
		out, err = r.Save(tx, id, o.Version, []byte(content), o.Name, o.Note)
		return err
	})
	return out, err
}

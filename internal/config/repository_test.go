package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func database(t *testing.T) (*store.DB, *Repository) {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "config.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewRepository(func(store.Tx, Kind, json.RawMessage) error { return nil })
}
func commit(t *testing.T, db *store.DB, fn func(store.Tx) error) error {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func create(t *testing.T, db *store.DB, r *Repository, id string) Object {
	t.Helper()
	var object Object
	if err := commit(t, db, func(tx store.Tx) error {
		var err error
		object, err = r.Create(tx, id, Scheme, id, "note", []byte(`{"value":1}`))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestCanonicalNumbersKeysWhitespaceAndPrecision(t *testing.T) {
	for _, input := range []string{`{"b":[true,null],"a":1}`, ` { "a" : 1.00, "b":[true, null] } `, `{"a":10e-1,"b":[true,null]}`} {
		canonical, err := Canonical([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(canonical) != `{"a":1,"b":[true,null]}` {
			t.Fatalf("canonical=%s", canonical)
		}
	}
	canonical, err := Canonical([]byte(`{"integer":9007199254740993,"zero":-0.00,"string":"  keep  "}`))
	if err != nil || !strings.Contains(string(canonical), "9007199254740993") || !strings.Contains(string(canonical), `"zero":0`) || !strings.Contains(string(canonical), "  keep  ") {
		t.Fatalf("lossy canonicalization: %s %v", canonical, err)
	}
	for _, input := range []string{`{"a":1,"a":2}`, `{"a":{"x":1,"x":2}}`, `{} {}`, `[1]`, `{"x":NaN}`, `{"x":1e999999}`, strings.Repeat("{\"x\":", 70) + "0" + strings.Repeat("}", 70)} {
		if _, err := Canonical([]byte(input)); !errors.Is(err, ErrContent) {
			t.Fatalf("invalid content accepted: %s", input)
		}
	}
}

func TestMetadataAndEquivalentContentDoNotIncreaseVersion(t *testing.T) {
	db, r := database(t)
	object := create(t, db, r, "o")
	if err := commit(t, db, func(tx store.Tx) error {
		updated, err := r.Save(tx, "o", 1, []byte(` {"value":1.0}`), "renamed", "new note")
		if err != nil {
			return err
		}
		if updated.Version != 1 || updated.Digest != object.Digest {
			t.Fatal("metadata/equivalent number increased content version")
		}
		history, err := r.History(tx, "o")
		if err != nil {
			return err
		}
		if len(history) != 1 || history[0].Number != 1 {
			t.Fatal("unchanged save created history")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVersionHistoryPruningRollbackAndConflict(t *testing.T) {
	db, r := database(t)
	create(t, db, r, "o")
	for version := uint64(1); version < 7; version++ {
		if err := commit(t, db, func(tx store.Tx) error {
			_, err := r.Save(tx, "o", version, []byte(`{"value":`+jsonNumber(version+1)+`}`), "name", "note")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := commit(t, db, func(tx store.Tx) error {
		history, err := r.History(tx, "o")
		if err != nil {
			return err
		}
		if len(history) != 5 || history[0].Number != 7 || history[4].Number != 3 {
			t.Fatalf("history=%v", history)
		}
		rolled, err := r.Rollback(tx, "o", 7, 3)
		if err != nil {
			return err
		}
		if rolled.Version != 8 || string(rolled.Content) != `{"value":3}` || rolled.Name != "name" || rolled.Note != "note" {
			t.Fatalf("rollback=%+v", rolled)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Save(tx, "o", 7, []byte(`{"value":99}`), "stale", "note")
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent stale save accepted: %v", err)
	}
}

func jsonNumber(n uint64) string { data, _ := json.Marshal(n); return string(data) }

func TestReadOnlyCopyAndImportConflictAreIndependent(t *testing.T) {
	db, r := database(t)
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.SeedBuiltin(tx, "builtin", RuleSet, "Built in", []byte(`{"rules":[]}`))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Save(tx, "builtin", 1, []byte(`{"rules":[]}`), "changed", "")
		return err
	}); !errors.Is(err, ErrReadOnly) {
		t.Fatal("builtin editable")
	}
	var copied Object
	if err := commit(t, db, func(tx store.Tx) error {
		var err error
		copied, err = r.Copy(tx, "builtin", "Editable copy")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if copied.ReadOnly || copied.Version != 1 || copied.ID == "builtin" {
		t.Fatal("copy retained provenance")
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Import(tx, Package{SchemaVersion: 1, Kind: RuleSet, Name: "Editable copy", Content: json.RawMessage(`{"rules":[]}`)})
		return err
	}); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("same-name import overwrote config: %v", err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		builtin, err := r.Get(tx, "builtin")
		if err != nil {
			return err
		}
		if builtin.Name != "Built in" || string(builtin.Content) != `{"rules":[]}` {
			t.Fatal("copy edits changed builtin")
		}
		_, err = r.Save(tx, copied.ID, 1, []byte(`{"rules":[1]}`), copied.Name, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Import(tx, Package{SchemaVersion: 2, Kind: RuleSet, Name: "future", Content: json.RawMessage(`{}`)})
		return err
	}); !errors.Is(err, ErrSchema) {
		t.Fatal("future config imported")
	}
}

func TestConfigListsAreBoundedAndSettingsHaveNoObjectHistory(t *testing.T) {
	db, r := database(t)
	create(t, db, r, "a")
	create(t, db, r, "b")
	if err := commit(t, db, func(tx store.Tx) error {
		first, err := r.List(tx, Scheme, "", 1)
		if err != nil {
			return err
		}
		second, err := r.List(tx, Scheme, first[0].ID, 1)
		if err != nil {
			return err
		}
		if len(first) != 1 || len(second) != 1 || first[0].ID != "a" || second[0].ID != "b" {
			t.Fatal("config paging lost objects")
		}
		if _, err = r.List(tx, Scheme, "", 201); !errors.Is(err, ErrContent) {
			t.Fatal("unbounded list accepted")
		}
		settings, err := r.Settings(tx)
		if err != nil {
			return err
		}
		if settings != DefaultSettings() || settings.AutomaticUpdateChecks {
			t.Fatal("default settings changed or auto update opted in silently")
		}
		settings.Theme = "dark"
		settings.CloseBehavior = "quit"
		if err = r.SaveSettings(tx, settings); err != nil {
			return err
		}
		loaded, err := r.Settings(tx)
		if err != nil {
			return err
		}
		if loaded != settings {
			t.Fatal("settings not persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidationFailureAndRollbackDoNotPersistConfigOrEvents(t *testing.T) {
	db, _ := database(t)
	validation := errors.New("fixture schema violation")
	r := NewRepository(func(store.Tx, Kind, json.RawMessage) error { return validation })
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "invalid", Scheme, "invalid", "", []byte(`{}`))
		return err
	}); !errors.Is(err, validation) {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := NewRepository(nil).Create(tx, "unvalidated", Scheme, "bad", "", []byte(`{}`))
		return err
	}); !errors.Is(err, ErrContent) {
		t.Fatal("nil validation bypassed")
	}
	r = NewRepository(func(store.Tx, Kind, json.RawMessage) error { return nil })
	rollback := errors.New("fixture rollback")
	if err := commit(t, db, func(tx store.Tx) error {
		if _, err := r.Create(tx, "rolled", Scheme, "rolled", "", []byte(`{}`)); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { _, err := r.Get(tx, "rolled"); return err }); !errors.Is(err, ErrMissing) {
		t.Fatal("rollback left object")
	}
	events, err := db.Pending(context.Background(), "query", 100)
	if err != nil || len(events) != 0 {
		t.Fatal("failed config emitted event")
	}
}

func TestDeleteRemovesAUserObjectAndItsHistoryButNotWhatIsInUseOrBuiltin(t *testing.T) {
	db, r := database(t)
	a := create(t, db, r, "a")
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.SeedBuiltin(tx, "b", Scheme, "built-in", []byte(`{"value":2}`))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "b", 1) }); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("built-in: %v", err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "a", 9) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "nope", 1) }); !errors.Is(err, ErrMissing) {
		t.Fatalf("unknown: %v", err)
	}
	// a preset that names the object keeps it alive
	if err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Create(tx, "p", Preset, "p", "", []byte(`{"ruleset_id":"x","scheme_id":"a","mode":"guided","rules_only":true,"memory_bytes":1,"dpi":200,"style":"black","retention_days":0}`))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "a", a.Version) }); !errors.Is(err, ErrInUse) {
		t.Fatalf("in use: %v", err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "p", 1) }); err != nil {
		t.Fatal(err)
	}
	if err := commit(t, db, func(tx store.Tx) error { return r.Delete(tx, "a", a.Version) }); err != nil {
		t.Fatal(err)
	}
	err := commit(t, db, func(tx store.Tx) error {
		_, err := r.Get(tx, "a")
		if !errors.Is(err, ErrMissing) {
			return errors.New("still there")
		}
		if _, err = r.History(tx, "a"); !errors.Is(err, ErrMissing) {
			return errors.New("history reachable")
		}
		// the id and the name are free again (a left-over history row would clash on version 1)
		_, err = r.Create(tx, "a", Scheme, "a", "", []byte(`{"value":3}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSettingsKeepTheNewPreferencesAndFillDefaultsForOlderData(t *testing.T) {
	db, r := database(t)
	if err := commit(t, db, func(tx store.Tx) error {
		// data written before the page kept style, tier and retention
		_, scope, _ := transaction(tx)
		_, err := scope.Exec(`INSERT INTO config_settings(key,value) VALUES('application',?)`, []byte(`{"close_behavior":"quit","theme":"dark","default_style":"solid","automatic_update_checks":true}`))
		if err != nil {
			return err
		}
		s, err := r.Settings(tx)
		if err != nil {
			return err
		}
		if s.RedactStyle != "black" || s.ResourceTier != "comfort" || s.RetentionDays != 7 || s.CloseBehavior != "quit" || !s.AutomaticUpdateChecks {
			t.Fatalf("%+v", s)
		}
		// "do not keep" (0) is a choice, not a missing value
		s.RetentionDays = 0
		if err = r.SaveSettings(tx, s); err != nil {
			return err
		}
		got, err := r.Settings(tx)
		if err != nil || got.RetentionDays != 0 || got.RedactStyle != "black" {
			t.Fatalf("%+v %v", got, err)
		}
		for name, bad := range map[string]func(*Settings){
			"style": func(x *Settings) { x.RedactStyle = "neon" }, "tier": func(x *Settings) { x.ResourceTier = "turbo" },
			"days": func(x *Settings) { x.RetentionDays = 5 }, "close": func(x *Settings) { x.CloseBehavior = "hide" },
		} {
			x := got
			bad(&x)
			if err := r.SaveSettings(tx, x); !errors.Is(err, ErrContent) {
				t.Errorf("%s: %v", name, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

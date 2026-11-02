package categories

import (
	"slices"
	"testing"
)

func TestStandardVocabularyIsValidAndOrdered(t *testing.T) {
	std := StandardVocabulary()
	ids := Catalog(std).IDs()
	want := []string{"person", "phone", "email", "id_number", "address", "payment_card", "date", "account", "ip_address", "url", "secret", "organization", "other"}
	if !slices.Equal(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	for _, c := range std {
		if c.Origin != Standard || c.Name == "" || c.Placeholder == "" || c.Color == "" {
			t.Fatalf("incomplete %+v", c)
		}
	}
	// a copy: changing it does not change the vocabulary
	std[0].Name = "x"
	if StandardVocabulary()[0].Name != "人名" {
		t.Fatal("vocabulary shared")
	}
}

func TestFromDeclsRefusesBadOrDuplicateEntries(t *testing.T) {
	ok := Decl{ID: "license_plate", Name: "车牌", Placeholder: "[车牌]"}
	if _, err := FromDecls([]Decl{ok}, Model); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string][]Decl{
		"upper id":       {{ID: "Plate", Name: "车牌", Placeholder: "[车牌]"}},
		"long id":        {{ID: "a234567890123456789012345678901234", Name: "车牌", Placeholder: "[车牌]"}},
		"no name":        {{ID: "plate", Placeholder: "[车牌]"}},
		"no placeholder": {{ID: "plate", Name: "车牌"}},
		"bad color":      {{ID: "plate", Name: "车牌", Placeholder: "[车牌]", Color: "red"}},
		"duplicate id":   {ok, {ID: "license_plate", Name: "牌照", Placeholder: "[牌照]"}},
		"duplicate name": {ok, {ID: "plate", Name: "车牌", Placeholder: "[车牌]"}},
	} {
		if _, err := FromDecls(d, Model); err != ErrInvalid {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := ParseVocabulary([]byte(`{"id":"v","categories":[{"id":"a","name":"A","placeholder":"[A]","extra":1}]}`)); err != ErrInvalid {
		t.Fatal("unknown member accepted")
	}
}

func TestMergeKeepsTheFirstDefinitionAndPlaceholdersCoverManual(t *testing.T) {
	model, _ := FromDecls([]Decl{{ID: "phone", Name: "号码", Placeholder: "[号码]"}, {ID: "license_plate", Name: "车牌", Placeholder: "[车牌]"}}, Model)
	cat := Merge(StandardVocabulary(), model)
	if c, _ := cat.Get("phone"); c.Name != "电话" || c.Origin != Standard {
		t.Fatalf("a model renamed a standard category: %+v", c)
	}
	if c, ok := cat.Get("license_plate"); !ok || c.Origin != Model || cat.IDs()[len(cat)-1] != "license_plate" {
		t.Fatalf("declared category missing or misplaced: %+v", c)
	}
	ph := cat.Placeholders([]string{"email", "license_plate", "unknown"})
	if ph["email"] != "[邮箱]" || ph["license_plate"] != "[车牌]" || ph["person"] != "[姓名]" || ph["other"] != "[已处理]" {
		t.Fatalf("placeholders = %v", ph)
	}
	if _, ok := ph["url"]; ok {
		t.Fatal("a category neither enabled nor hand-markable got a placeholder")
	}
	if _, ok := ph["unknown"]; ok {
		t.Fatal("an undefined id got a placeholder")
	}
}

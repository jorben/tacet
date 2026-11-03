// Package categories is the category catalog: what a finding can be reported as, and how each category
// is named and replaced. Categories are data, not code. The standard vocabulary ships as a file of this
// package; a model's manifest maps its labels onto it and may declare categories of its own; users
// create further categories in the workspace. Rules, schemes, findings and the plan refer to a category
// by its id only (architecture document, 方案与类别).
package categories

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"unicode/utf8"
)

// Origin says where a category is defined.
type Origin string

const (
	Standard Origin = "standard" // the shipped vocabulary
	Model    Origin = "model"    // declared by a model's manifest
	Custom   Origin = "custom"   // created by the user in the workspace
)

// Category is one entry of the catalog.
type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Placeholder string `json:"placeholder"`
	Color       string `json:"color,omitempty"`
	// Manual: offered when a person marks a selection by hand, even if no source of the scheme detects it.
	Manual bool `json:"manual,omitempty"`
	// ReviewOnly: a category a person marks in review but a scheme does not list (company names, other)
	// unless a model or rule of the scheme reports it.
	ReviewOnly bool   `json:"review_only,omitempty"`
	Origin     Origin `json:"origin"`
}

var (
	ErrInvalid = errors.New("CATEGORY_INVALID")

	// Shipped and model-declared ids are short readable words; user categories use their config object id.
	idPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

const (
	MaxName        = 32
	MaxPlaceholder = 64
	// MaxID bounds every category id, the limit of a manual mark's category.
	MaxID = 32
)

// Decl is a category as a file declares it (the standard vocabulary or a model manifest).
type Decl struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Placeholder string `json:"placeholder"`
	Color       string `json:"color,omitempty"`
	Manual      bool   `json:"manual,omitempty"`
	ReviewOnly  bool   `json:"review_only,omitempty"`
}

// CheckText validates a display name and placeholder (shared with user categories).
func CheckText(name, placeholder, color string) error {
	if n := utf8.RuneCountInString(name); n == 0 || n > MaxName || !utf8.ValidString(name) {
		return ErrInvalid
	}
	if n := utf8.RuneCountInString(placeholder); n == 0 || n > MaxPlaceholder || !utf8.ValidString(placeholder) {
		return ErrInvalid
	}
	if color != "" && !colorPattern.MatchString(color) {
		return ErrInvalid
	}
	return nil
}

// FromDecls checks declarations and returns them as categories of the given origin. Ids and names are
// unique within the list.
func FromDecls(decls []Decl, origin Origin) ([]Category, error) {
	out := make([]Category, 0, len(decls))
	ids, names := map[string]bool{}, map[string]bool{}
	for _, d := range decls {
		if !idPattern.MatchString(d.ID) || CheckText(d.Name, d.Placeholder, d.Color) != nil || ids[d.ID] || names[d.Name] {
			return nil, ErrInvalid
		}
		ids[d.ID], names[d.Name] = true, true
		out = append(out, Category{ID: d.ID, Name: d.Name, Placeholder: d.Placeholder, Color: d.Color, Manual: d.Manual, ReviewOnly: d.ReviewOnly, Origin: origin})
	}
	return out, nil
}

//go:embed standard-v1.json
var standardFile []byte

var standard = sync.OnceValues(func() ([]Category, error) { return ParseVocabulary(standardFile) })

// ParseVocabulary reads a vocabulary file: {"id": ..., "categories": [...]}. Unknown members are refused.
func ParseVocabulary(data []byte) ([]Category, error) {
	var doc struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Categories  []Decl `json:"categories"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&doc) != nil || doc.ID == "" || len(doc.Categories) == 0 || dec.More() {
		return nil, ErrInvalid
	}
	return FromDecls(doc.Categories, Standard)
}

// StandardVocabulary returns a copy of the shipped vocabulary. The file is part of the build; a broken
// one is a build defect that the package tests catch.
func StandardVocabulary() []Category {
	c, err := standard()
	if err != nil {
		panic("categories: standard vocabulary: " + err.Error())
	}
	return append([]Category(nil), c...)
}

// Catalog is an ordered list of categories with unique ids.
type Catalog []Category

// Merge joins lists in order. The first definition of an id wins: a model cannot rename a standard
// category, and a user category cannot shadow either.
func Merge(lists ...[]Category) Catalog {
	var out Catalog
	seen := map[string]bool{}
	for _, l := range lists {
		for _, c := range l {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// Get returns the category with this id.
func (c Catalog) Get(id string) (Category, bool) {
	for _, x := range c {
		if x.ID == id {
			return x, true
		}
	}
	return Category{}, false
}

// Has reports whether the catalog defines id.
func (c Catalog) Has(id string) bool {
	_, ok := c.Get(id)
	return ok
}

// IDs returns the ids in catalog order.
func (c Catalog) IDs() []string {
	out := make([]string, len(c))
	for i, x := range c {
		out[i] = x.ID
	}
	return out
}

// Placeholders maps the given ids, and every category a person can mark by hand, to their placeholder
// text. Ids the catalog does not define are left out (the renderer's fallback applies).
func (c Catalog) Placeholders(ids []string) map[string]string {
	out := map[string]string{}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, x := range c {
		if want[x.ID] || x.Manual {
			out[x.ID] = x.Placeholder
		}
	}
	return out
}

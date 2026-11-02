package app

import (
	"context"
	"encoding/json"

	"github.com/jorben/tacet/internal/categories"
	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/store"
)

// CategoryInfo is one entry of the category catalog as the screens and the CLI see it. Rules, schemes
// and findings carry only the id; the name, placeholder and color come from here.
type CategoryInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Placeholder string `json:"placeholder"`
	Color       string `json:"color,omitempty"`
	// Manual: offered when a selection is marked by hand, whether or not the scheme detects it.
	Manual bool `json:"manual,omitempty"`
	// Origin: "standard" (shipped vocabulary), "model" (declared by a model's manifest) or "custom".
	Origin string `json:"origin"`
	// Version of a user category's stored object (ConfigSave / ConfigDelete check it); 0 otherwise.
	Version uint64 `json:"version,omitempty"`
}

// userCategories are the categories the user created: config objects of kind category. The object id is
// the category id, its name the category's name. A person can mark any of them by hand.
func (e *Engine) userCategories(tx store.Tx) ([]categories.Category, map[string]uint64, error) {
	var out []categories.Category
	versions := map[string]uint64{}
	for after := ""; len(out) < maxConfigObjects; {
		page, err := e.cfg.List(tx, config.Category, after, 200)
		if err != nil || len(page) == 0 {
			return out, versions, err
		}
		for _, s := range page {
			after = s.ID
			o, err := e.cfg.Get(tx, s.ID)
			if err != nil {
				return nil, nil, err
			}
			var c config.CategoryContent
			if json.Unmarshal(o.Content, &c) != nil {
				continue
			}
			out = append(out, categories.Category{ID: o.ID, Name: o.Name, Placeholder: c.Placeholder, Color: c.Color, Manual: true, Origin: categories.Custom})
			versions[o.ID] = o.Version
		}
	}
	return out, versions, nil
}

// builtinCategories are the standard vocabulary, then the categories the catalog's models declare.
func (e *Engine) builtinCategories() categories.Catalog {
	lists := [][]categories.Category{categories.StandardVocabulary()}
	for _, m := range e.models {
		lists = append(lists, m.declared)
	}
	return categories.Merge(lists...)
}

// categoryCatalog is the category catalog of this installation and workspace: the standard vocabulary,
// the categories the catalog's models declare, then the user's categories. The first definition of an id
// wins.
func (e *Engine) categoryCatalog(tx store.Tx) (categories.Catalog, error) {
	user, _, err := e.userCategories(tx)
	if err != nil {
		return nil, err
	}
	return categories.Merge(e.builtinCategories(), user), nil
}

// checkCategory validates a user category before it is stored: a name and placeholder within bounds, and a
// name no other category of the catalog has, so that two categories are never shown alike (the store keeps
// names unique among user categories; this adds the shipped and model-declared ones).
func (e *Engine) checkCategory(tx store.Tx, id, name string, content []byte) error {
	var c config.CategoryContent
	if json.Unmarshal(content, &c) != nil || categories.CheckText(name, c.Placeholder, c.Color) != nil {
		return config.ErrContent
	}
	for _, b := range e.builtinCategories() {
		if b.Name == name || b.ID == id {
			return config.ErrNameConflict
		}
	}
	return nil
}

// checkKind runs the checks a kind needs beyond its schema before it is stored.
func (e *Engine) checkKind(tx store.Tx, kind config.Kind, id, name string, content []byte) error {
	switch kind {
	case config.Category:
		return e.checkCategory(tx, id, name, content)
	case config.ModelMapping:
		return e.checkMapping(tx, name, content)
	}
	return nil
}

// checkMapping validates a model mapping: it is named after a model of the catalog, maps only labels that
// model emits, and maps them to categories of the catalog. Labels left out are not used.
func (e *Engine) checkMapping(tx store.Tx, name string, content []byte) error {
	var m config.MappingContent
	if json.Unmarshal(content, &m) != nil || m.ModelID != name {
		return config.ErrContent
	}
	model := e.modelByID(m.ModelID)
	if model == nil {
		return config.ErrContent
	}
	cat, err := e.categoryCatalog(tx)
	if err != nil {
		return err
	}
	for label, c := range m.LabelMap {
		if _, known := model.labels[label]; !known || !cat.Has(c) {
			return config.ErrContent
		}
	}
	return nil
}

// effectiveLabels is the label map a model runs with now: the user's mapping when there is one, else the
// default from the manifest.
func (e *Engine) effectiveLabels(tx store.Tx, m *modelInfo) (map[string]string, error) {
	o, ok, err := e.cfg.GetByName(tx, config.ModelMapping, m.id)
	if err != nil || !ok {
		return m.labels, err
	}
	var mc config.MappingContent
	if json.Unmarshal(o.Content, &mc) != nil {
		return nil, config.ErrContent
	}
	return mc.LabelMap, nil
}

// Categories lists the category catalog in display order.
func (e *Engine) Categories(ctx context.Context) ([]CategoryInfo, error) {
	var cat categories.Catalog
	var versions map[string]uint64
	err := e.ws.Commit(ctx, func(tx store.Tx) (err error) {
		var user []categories.Category
		if user, versions, err = e.userCategories(tx); err != nil {
			return err
		}
		cat = categories.Merge(e.builtinCategories(), user)
		return nil
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]CategoryInfo, 0, len(cat))
	for _, c := range cat {
		out = append(out, CategoryInfo{ID: c.ID, Name: c.Name, Placeholder: c.Placeholder, Color: c.Color, Manual: c.Manual, Origin: string(c.Origin), Version: versions[c.ID]})
	}
	return out, nil
}

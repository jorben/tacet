package rules

import (
	"bytes"
	"encoding/json"
	"errors"
)

var ErrPack = errors.New("RULE_PACK_INVALID")

// ParsePack reads a rule pack document: {"id": ..., "rules": [...]} with only
// the declarative Rule fields. Unknown members are refused, so a pack cannot
// smuggle in anything executable (architecture 8.1).
func ParsePack(data []byte) (id string, out []Rule, err error) {
	var doc struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Rules       []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"` // display name only; detection does not use it
			Category  string   `json:"category"`
			Pattern   string   `json:"pattern"`
			Terms     []string `json:"terms"`
			Boundary  string   `json:"boundary"`
			Validator string   `json:"validator"`
			Suspect   bool     `json:"suspect"`
			FoldCase  bool     `json:"fold_case"`
			Context   []string `json:"context"`
			Window    int      `json:"context_window"`
		} `json:"rules"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&doc); err != nil || doc.ID == "" || dec.More() {
		return "", nil, ErrPack
	}
	for _, r := range doc.Rules {
		out = append(out, Rule{ID: r.ID, Category: r.Category, Pattern: r.Pattern, Terms: r.Terms,
			Boundary: r.Boundary, Validator: r.Validator, Suspect: r.Suspect, FoldCase: r.FoldCase, Context: r.Context, ContextWindow: r.Window})
	}
	return doc.ID, out, nil
}

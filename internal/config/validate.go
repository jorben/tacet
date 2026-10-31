package config

import (
	"bytes"
	"encoding/json"

	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// RuleContent is the versioned declarative rule representation. Arbitrary code,
// validators and remote references cannot be supplied in a configuration.
type RuleContent struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Pattern   string   `json:"pattern,omitempty"`
	Terms     []string `json:"terms,omitempty"`
	Boundary  string   `json:"boundary,omitempty"`
	Validator string   `json:"validator,omitempty"`
	Suspect   bool     `json:"suspect,omitempty"`
	FoldCase  bool     `json:"fold_case,omitempty"`
	Context   []string `json:"context,omitempty"`
	Window    int      `json:"context_window,omitempty"`
	// Name is what the person calls the rule; detection does not use it.
	Name string `json:"name,omitempty"`
	// Disabled keeps the rule in the set but out of detection (a switch the person can turn back on).
	Disabled bool `json:"disabled,omitempty"`
}

type RulesContent struct {
	Rules []RuleContent `json:"rules"`
}

// Enabled lists the rules that take part in detection.
func (c RulesContent) Enabled() []RuleContent {
	out := make([]RuleContent, 0, len(c.Rules))
	for _, r := range c.Rules {
		if !r.Disabled {
			out = append(out, r)
		}
	}
	return out
}

type SchemeContent struct {
	CategoryActions      map[string]string `json:"category_actions"`
	ImageDefault         string            `json:"image_default"`
	HumanReview          []string          `json:"human_review,omitempty"`
	TypeChangeAuthorized bool              `json:"type_change_authorized,omitempty"`
	AutoMismatch         bool              `json:"auto_mismatch,omitempty"`
}

type ModelContent struct {
	ModelID        string            `json:"model_id"`
	ManifestDigest string            `json:"manifest_digest"`
	Backend        string            `json:"backend"`
	AdapterFloor   float64           `json:"adapter_floor"`
	Calibrated     float64           `json:"calibrated"`
	LabelMap       map[string]string `json:"label_map"`
	MemoryBytes    int64             `json:"memory_bytes"`
}

type PresetContent struct {
	RuleSetID string `json:"ruleset_id"`
	// ExtraRuleSetIDs are further rule sets whose rules run together with RuleSetID's.
	ExtraRuleSetIDs []string `json:"extra_ruleset_ids,omitempty"`
	SchemeID        string   `json:"scheme_id"`
	// SchemeRev is the version of the scheme object this preset version was saved with, so that going
	// back to a preset version can bring its scheme back too. 0: not recorded.
	SchemeRev      uint64  `json:"scheme_rev,omitempty"`
	ModelProfileID string  `json:"model_profile_id,omitempty"`
	Mode           string  `json:"mode"`
	RulesOnly      bool    `json:"rules_only"`
	MinConfidence  float64 `json:"min_confidence,omitempty"`
	MemoryBytes    int64   `json:"memory_bytes"`
	DPI            int     `json:"dpi"`
	Style          string  `json:"style"`
	RetentionDays  int     `json:"retention_days"`
}

type rejectRemote struct{}

func (rejectRemote) Load(string) (any, error) { return nil, ErrSchema }

// Validator compiles the embedded local contract once. It only resolves
// references from config's own store in the supplied transaction, never URLs.
type Validator struct {
	schemas map[Kind]*jsonschema.Schema
	budget  rules.Budget
}

func NewValidator(budget rules.Budget) (*Validator, error) {
	data, err := protocol.Schemas.ReadFile("schema/v1/config.schema.json")
	if err != nil {
		return nil, ErrSchema
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, ErrSchema
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(rejectRemote{})
	const base = "https://github.com/jorben/tacet/protocol/schema/v1/config.schema.json"
	if err = compiler.AddResource(base, doc); err != nil {
		return nil, ErrSchema
	}
	validator := &Validator{schemas: map[Kind]*jsonschema.Schema{}, budget: budget}
	for _, kind := range []Kind{RuleSet, Scheme, Preset, ModelProfile} {
		schema, err := compiler.Compile(base + "#/$defs/" + string(kind))
		if err != nil {
			return nil, ErrSchema
		}
		validator.schemas[kind] = schema
	}
	return validator, nil
}

func (v *Validator) Validate(tx store.Tx, kind Kind, content json.RawMessage) error {
	schema := v.schemas[kind]
	if schema == nil {
		return ErrSchema
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(content))
	if err != nil {
		return ErrContent
	}
	if err = schema.Validate(value); err != nil {
		return ErrContent
	} // validator errors may include secret values
	switch kind {
	case RuleSet:
		var object RulesContent
		if json.Unmarshal(content, &object) != nil {
			return ErrContent
		}
		if len(object.Enabled()) == 0 {
			return nil
		} // an empty rule set is valid; category compatibility is checked at Start
		compiled := make([]rules.Rule, 0, len(object.Rules))
		for _, rule := range object.Enabled() {
			compiled = append(compiled, rules.Rule{ID: rule.ID, Category: rule.Category, Pattern: rule.Pattern, Terms: rule.Terms, Boundary: rule.Boundary, Validator: rule.Validator, Suspect: rule.Suspect, FoldCase: rule.FoldCase, Context: rule.Context, ContextWindow: rule.Window})
		}
		_, err := rules.Compile(compiled, v.budget)
		return err
	case ModelProfile:
		var object ModelContent
		if json.Unmarshal(content, &object) != nil || object.Calibrated < object.AdapterFloor {
			return ErrContent
		}
	case Scheme:
		var object SchemeContent
		if json.Unmarshal(content, &object) != nil {
			return ErrContent
		}
		for _, category := range object.HumanReview {
			if _, exists := object.CategoryActions[category]; !exists {
				return ErrContent
			}
		}
	case Preset:
		var object PresetContent
		if json.Unmarshal(content, &object) != nil {
			return ErrContent
		}
		for _, reference := range []struct {
			id   string
			kind Kind
		}{{object.RuleSetID, RuleSet}, {object.SchemeID, Scheme}} {
			// (extra rule sets are checked below)
			ref, err := (&Repository{}).Get(tx, reference.id)
			if err != nil {
				return err
			}
			if ref.Kind != reference.kind {
				return ErrContent
			}
		}
		for _, id := range object.ExtraRuleSetIDs {
			ref, err := (&Repository{}).Get(tx, id)
			if err != nil {
				return err
			}
			if ref.Kind != RuleSet || id == object.RuleSetID {
				return ErrContent
			}
		}
		if object.ModelProfileID != "" {
			model, err := modelFor(tx, object.ModelProfileID)
			if err != nil {
				return err
			}
			if !object.RulesOnly && (object.MinConfidence < model.AdapterFloor || object.MinConfidence <= 0) {
				return ErrContent
			}
		}
	}
	return nil
}

func modelFor(tx store.Tx, id string) (ModelContent, error) {
	object, err := (&Repository{}).Get(tx, id)
	if err != nil {
		return ModelContent{}, err
	}
	var model ModelContent
	if object.Kind != ModelProfile || json.Unmarshal(object.Content, &model) != nil {
		return ModelContent{}, ErrContent
	}
	return model, nil
}

// ResetModelThreshold returns the calibrated default when the profile reference
// changes. UI uses Object.MinConfidenceReset to announce this persisted change.
func resetModelThreshold(tx store.Tx, old, input []byte) ([]byte, bool, error) {
	var previous, next map[string]json.RawMessage
	if json.Unmarshal(old, &previous) != nil || json.Unmarshal(input, &next) != nil {
		return nil, false, ErrContent
	}
	var oldID, newID string
	if raw, ok := previous["model_profile_id"]; ok && json.Unmarshal(raw, &oldID) != nil {
		return nil, false, ErrContent
	}
	if raw, ok := next["model_profile_id"]; ok && json.Unmarshal(raw, &newID) != nil {
		return nil, false, ErrContent
	}
	if oldID == newID || newID == "" {
		return input, false, nil
	}
	model, err := modelFor(tx, newID)
	if err != nil {
		return nil, false, err
	}
	threshold, err := json.Marshal(model.Calibrated)
	if err != nil {
		return nil, false, ErrContent
	}
	next["min_confidence"] = threshold
	encoded, err := json.Marshal(next)
	return encoded, true, err
}

package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/store"
)

var ErrBinding = errors.New("SNAPSHOT_BINDING_INVALID")

// Bindings are the concrete registry and model-catalog facts for the selected
// files. They must be supplied by the app's Start use case, not by a plugin.
// A downloaded model alone does not make its session ready; CheckBinding in
// models remains a separate prerequisite before dispatch.
type Bindings struct {
	Environment  Env
	Runtime      RuntimeContent
	ModelDigests map[string]string // model id -> exact catalog manifest digest
	Delivery     DeliveryContent
	// Style is the application's redaction style, used by a preset whose style is "default".
	Style string
}

func configRef(object config.Object) ConfigRef {
	return ConfigRef{ID: object.ID, Version: object.Version, Digest: object.Digest}
}

func readObject(tx store.Tx, repo *config.Repository, id string, kind config.Kind) (config.Object, error) {
	object, err := repo.Get(tx, id)
	if err != nil {
		return config.Object{}, err
	}
	if object.Kind != kind {
		return config.Object{}, ErrBinding
	}
	return object, nil
}

// ResolvePreset reads the preset and its references at their current versions
// in one config transaction. Full resolved contents enter the facets, so later
// history pruning cannot change an existing task's executable configuration.
// Nothing is written here: app must check the report, record any user-approved
// narrowing and grants, then Freeze/Save alongside batch confirmation.
func ResolvePreset(tx store.Tx, repo *config.Repository, presetID string, b Bindings) (Content, CompatReport, error) {
	if repo == nil || b.Runtime.Protocol != "1.0" || len(b.Environment.Formats) == 0 {
		return Content{}, CompatReport{}, ErrBinding
	}
	presetObject, err := readObject(tx, repo, presetID, config.Preset)
	if err != nil {
		return Content{}, CompatReport{}, err
	}
	var preset config.PresetContent
	if json.Unmarshal(presetObject.Content, &preset) != nil {
		return Content{}, CompatReport{}, ErrBinding
	}
	ruleObject, err := readObject(tx, repo, preset.RuleSetID, config.RuleSet)
	if err != nil {
		return Content{}, CompatReport{}, err
	}
	schemeObject, err := readObject(tx, repo, preset.SchemeID, config.Scheme)
	if err != nil {
		return Content{}, CompatReport{}, err
	}
	var ruleContent config.RulesContent
	var scheme config.SchemeContent
	if json.Unmarshal(ruleObject.Content, &ruleContent) != nil || json.Unmarshal(schemeObject.Content, &scheme) != nil {
		return Content{}, CompatReport{}, ErrBinding
	}
	ruleDigest, ruleJSON := ruleObject.Digest, ruleObject.Content
	var extras []ConfigRef
	if len(preset.ExtraRuleSetIDs) > 0 {
		// Rule sets that run together become one rule list; a rule id used twice would make findings
		// ambiguous, so it is refused rather than renamed.
		seen := map[string]bool{}
		for _, r := range ruleContent.Rules {
			seen[r.ID] = true
		}
		h := sha256.New()
		h.Write([]byte(ruleObject.Digest))
		for _, id := range preset.ExtraRuleSetIDs {
			extra, err := readObject(tx, repo, id, config.RuleSet)
			if err != nil {
				return Content{}, CompatReport{}, err
			}
			var more config.RulesContent
			if json.Unmarshal(extra.Content, &more) != nil {
				return Content{}, CompatReport{}, ErrBinding
			}
			for _, r := range more.Rules {
				if seen[r.ID] {
					return Content{}, CompatReport{}, ErrBinding
				}
				seen[r.ID] = true
			}
			ruleContent.Rules = append(ruleContent.Rules, more.Rules...)
			extras = append(extras, configRef(extra))
			h.Write([]byte{0})
			h.Write([]byte(extra.Digest))
		}
		merged, err := json.Marshal(ruleContent)
		if err != nil {
			return Content{}, CompatReport{}, ErrBinding
		}
		ruleDigest, ruleJSON = hex.EncodeToString(h.Sum(nil)), merged
	}
	content := Content{
		Detect:  DetectContent{RuleSetDigest: ruleDigest, RuleSet: configRef(ruleObject), ExtraRuleSets: extras, RuleContent: append(json.RawMessage(nil), ruleJSON...), RulesOnly: preset.RulesOnly},
		Dispose: DisposeContent{Scheme: configRef(schemeObject), SchemeContent: append(json.RawMessage(nil), schemeObject.Content...), CategoryAction: scheme.CategoryActions, MinConfidence: preset.MinConfidence, ImageDefault: scheme.ImageDefault, HumanReview: scheme.HumanReview, Mode: preset.Mode},
		Render:  RenderContent{Style: renderStyle(preset.Style, b.Style), DPI: preset.DPI},
		Runtime: b.Runtime, Delivery: b.Delivery,
	}
	content.Delivery.RetentionDays = preset.RetentionDays
	for category := range scheme.CategoryActions {
		content.Detect.Categories = append(content.Detect.Categories, category)
	}
	sort.Strings(content.Detect.Categories)
	// Compute available rule categories from the frozen rule content rather
	// than accepting stale UI claims about enabled detection coverage.
	environment := b.Environment
	environment.RuleCategories = make(map[string]bool)
	for _, rule := range ruleContent.Enabled() {
		environment.RuleCategories[rule.Category] = true
	}
	environment.MemoryBudgetBytes = preset.MemoryBytes
	if preset.ModelProfileID != "" {
		modelObject, err := readObject(tx, repo, preset.ModelProfileID, config.ModelProfile)
		if err != nil {
			return Content{}, CompatReport{}, err
		}
		var model config.ModelContent
		if json.Unmarshal(modelObject.Content, &model) != nil {
			return Content{}, CompatReport{}, ErrBinding
		}
		if !preset.RulesOnly {
			if b.ModelDigests[model.ModelID] != model.ManifestDigest {
				return Content{}, CompatReport{}, ErrBinding
			}
			catalog, ok := environment.Models[model.ModelID]
			if !ok || catalog.AdapterFloor != model.AdapterFloor {
				return Content{}, CompatReport{}, ErrBinding
			}
			for _, category := range model.LabelMap {
				if !contains(catalog.Categories, category) {
					return Content{}, CompatReport{}, ErrBinding
				}
			}
		}
		content.Detect.ModelID = model.ModelID
		content.Detect.ModelProfile = configRef(modelObject)
		content.Detect.ModelDigest = model.ManifestDigest
		content.Detect.Backend = model.Backend
		content.Detect.LabelMap = model.LabelMap
		content.Detect.AdapterFloor = model.AdapterFloor
		content.Detect.Calibrated = model.Calibrated
	}
	for _, format := range environment.Formats {
		plugin, ok := content.Runtime.Formats[format]
		if !ok || plugin.ID == "" || plugin.Digest == "" || plugin.Env == "" {
			return Content{}, CompatReport{}, ErrBinding
		}
	}
	// Detach every map/slice owned by registry/caller before returning it.
	detached, err := clone(content)
	if err != nil {
		return Content{}, CompatReport{}, err
	}
	return detached, Check(detached, environment), nil
}

// renderStyle is the concrete style a task is frozen with: the preset's, or for "default" the application's
// at the moment the task starts (requirements FR-29, INV-03), or black when neither names one.
func renderStyle(preset, application string) string {
	if preset != "default" && preset != "" {
		return preset
	}
	if application != "" {
		return application
	}
	return "black"
}

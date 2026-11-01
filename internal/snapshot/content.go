package snapshot

import "encoding/json"

// ConfigRef pins the content version of a configuration object. Display names
// and notes do not affect stage invalidation.
type ConfigRef struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
}

// The content types below are the resolved, concrete values of one run: no
// references to configuration objects that could change later (FR-19, INV-03).
// Maps are fine here: encoding/json writes map keys in sorted order, so the
// canonical encoding that feeds the digests is deterministic.

// DetectContent is read by extraction/detection, supplement scans and the
// re-detection of validation.
type DetectContent struct {
	ModelID         string            `json:"model_id"`     // empty in rules-only mode
	ModelDigest     string            `json:"model_digest"` // from the signed integrity manifest
	Backend         string            `json:"backend"`
	LabelMap        map[string]string `json:"label_map"`     // model label -> Tacet category
	AdapterFloor    float64           `json:"adapter_floor"` // lowest score the adapter returns
	Calibrated      float64           `json:"calibrated"`    // default minimum confidence of the model
	RuleSetDigest   string            `json:"rule_set_digest"`
	RuleSet         ConfigRef         `json:"ruleset"`
	ExtraRuleSets   []ConfigRef       `json:"extra_rulesets,omitempty"` // further rule sets merged into RuleContent
	RuleContent     json.RawMessage   `json:"rule_content"`
	ModelProfile    ConfigRef         `json:"model_profile"`
	RulePacks       map[string]string `json:"rule_packs"` // pack id -> version digest
	Categories      []string          `json:"categories"` // enabled
	RulesOnly       bool              `json:"rules_only"`
	FieldRuleDigest string            `json:"field_rule_digest"`
	// Execution is the build of the model that runs when it is not the profile's own (the optional
	// accelerated variant). It is frozen with the batch, so a resumed run uses the same weights and
	// execution provider, never a silent replacement.
	Execution *ModelExecution `json:"execution,omitempty"`
}

// ModelExecution names one build of the profile's model: its id, manifest digest and execution provider.
type ModelExecution struct {
	ModelID string `json:"model_id"`
	Digest  string `json:"digest"`
	Backend string `json:"backend"`
}

// DisposeContent is read by the resolve stage only.
type DisposeContent struct {
	Scheme              ConfigRef         `json:"scheme"`
	SchemeContent       json.RawMessage   `json:"scheme_content"`
	CategoryAction      map[string]string `json:"category_action"`
	MinConfidence       float64           `json:"min_confidence"`
	KeepExceptionDigest string            `json:"keep_exception_digest"`
	ImageDefault        string            `json:"image_default"`
	HumanReview         []string          `json:"human_review"`
	Mode                string            `json:"mode"` // guided, unattended, strict
}

// RenderContent is read when the plan is built and the candidate is written.
type RenderContent struct {
	Style       string `json:"style"`
	Placeholder string `json:"placeholder"`
	MaskForm    string `json:"mask_form"`
	DPI         int    `json:"dpi"`
	Output      string `json:"output"`
}

// PluginRef pins one plugin version and its environment.
type PluginRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Env     string `json:"env"`
}

// RuntimeContent is read by every stage that calls a plugin.
type RuntimeContent struct {
	Protocol string               `json:"protocol"`
	Formats  map[string]PluginRef `json:"formats"` // format -> chosen implementation
	Grants   []string             `json:"grants"`  // plugin digests with a completion grant (FR-44)
}

// DeliveryContent is read by publish and retention.
type DeliveryContent struct {
	OutputDir     string `json:"output_dir"`
	Naming        string `json:"naming"`
	PublishPolicy string `json:"publish_policy"`
	PartialRule   string `json:"partial_rule"`
	RetentionDays int    `json:"retention_days"`
}

// Content is everything a snapshot freezes.
type Content struct {
	Detect   DetectContent   `json:"detect"`
	Dispose  DisposeContent  `json:"dispose"`
	Render   RenderContent   `json:"render"`
	Runtime  RuntimeContent  `json:"runtime"`
	Delivery DeliveryContent `json:"delivery"`
}

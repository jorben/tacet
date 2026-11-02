package snapshot

import "sort"

// Issue codes of the compatibility check.
const (
	IssueNoDetectionSource   = "no_detection_source"       // blocking
	IssueModelDependent      = "model_dependent_category"  // needs narrowing (FR-18)
	IssueMemoryOverBudget    = "model_memory_over_budget"  // blocking (PF-06, PF-07)
	IssueMinConfidence       = "min_confidence_invalid"    // blocking (MD-12)
	IssueModelUnknown        = "model_not_in_catalog"      // blocking
	IssueFormatAction        = "format_action_unsupported" // warning, becomes an exception (BUX-02)
	IssueUnattendedPlugin    = "unattended_plugin_without_grant"
	IssueUnattendedPack      = "unattended_third_party_pack_only"
	IssueUnattendedThreshold = "unattended_threshold_above_default"
)

// Model is a catalog entry from the signed integrity manifest.
type Model struct {
	Categories   []string // categories its label map can produce
	AdapterFloor float64
	MemoryBytes  int64
}

// Env is what the check compares the content with. It is plain data supplied
// by registry, models and the run: Check does no I/O (MD-02).
type Env struct {
	Models             map[string]Model
	RuleCategories     map[string]bool            // covered by rule set, dictionary, validators or field rules
	PackOnlyCategories map[string]bool            // covered only by third-party rule packs (FR-46)
	MemoryBudgetBytes  int64                      // what the resource profile leaves for the model
	Formats            []string                   // member formats of the batch
	FormatActions      map[string]map[string]bool // format -> supported dispose action
	ThirdPartyDigests  map[string]bool            // plugin digests that are not built in
	// Categories are the ids of the category catalog: a model mapping may map labels to any of them.
	Categories map[string]bool
}

func issue(code string, kv ...string) Issue {
	p := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i]] = kv[i+1]
	}
	return Issue{Code: code, Params: p}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Check is pure: it returns the same report for the same inputs. Params hold
// category, format and plugin names only, never document content (INV-07).
func Check(c Content, env Env) CompatReport {
	var r CompatReport
	cats := append([]string(nil), c.Detect.Categories...)
	sort.Strings(cats)

	usesModel := c.Detect.ModelID != "" && !c.Detect.RulesOnly
	var model Model
	if usesModel {
		m, ok := env.Models[c.Detect.ModelID]
		if !ok {
			r.Blocking = append(r.Blocking, issue(IssueModelUnknown, "model", c.Detect.ModelID))
			usesModel = false
		} else {
			model = m
		}
	}
	// Every enabled category needs a detection source in this mode (FR-18).
	for _, cat := range cats {
		byRules := env.RuleCategories[cat]
		byModel := usesModel && contains(model.Categories, cat)
		switch {
		case byRules || byModel:
		case c.Detect.RulesOnly && c.Detect.ModelID != "":
			// A model exists but the user chose rules-only: narrow, don't claim coverage.
			r.NeedsNarrowing = append(r.NeedsNarrowing, issue(IssueModelDependent, "category", cat))
		default:
			r.Blocking = append(r.Blocking, issue(IssueNoDetectionSource, "category", cat))
		}
	}
	if usesModel {
		if env.MemoryBudgetBytes > 0 && model.MemoryBytes > env.MemoryBudgetBytes {
			r.Blocking = append(r.Blocking, issue(IssueMemoryOverBudget, "model", c.Detect.ModelID))
		}
		if c.Dispose.MinConfidence <= 0 || c.Dispose.MinConfidence < model.AdapterFloor {
			r.Blocking = append(r.Blocking, issue(IssueMinConfidence, "model", c.Detect.ModelID))
		}
	}
	formats := append([]string(nil), env.Formats...)
	sort.Strings(formats)
	actions := make([]string, 0, len(c.Dispose.CategoryAction))
	for _, a := range c.Dispose.CategoryAction {
		if !contains(actions, a) {
			actions = append(actions, a)
		}
	}
	sort.Strings(actions)
	for _, f := range formats {
		for _, a := range actions {
			if a == "keep" {
				continue
			}
			if !env.FormatActions[f][a] {
				r.Warnings = append(r.Warnings, issue(IssueFormatAction, "format", f, "action", a))
			}
		}
	}
	if c.Dispose.Mode == "unattended" {
		for _, f := range formats {
			p, ok := c.Runtime.Formats[f]
			if ok && env.ThirdPartyDigests[p.Digest] && !contains(c.Runtime.Grants, p.Digest) {
				r.NeedsNarrowing = append(r.NeedsNarrowing, issue(IssueUnattendedPlugin, "format", f, "plugin", p.ID))
			}
		}
		for _, cat := range cats {
			if env.PackOnlyCategories[cat] {
				r.NeedsNarrowing = append(r.NeedsNarrowing, issue(IssueUnattendedPack, "category", cat))
			}
			if usesModel && contains(model.Categories, cat) && !env.RuleCategories[cat] && c.Dispose.MinConfidence > c.Detect.Calibrated {
				r.NeedsNarrowing = append(r.NeedsNarrowing, issue(IssueUnattendedThreshold, "category", cat))
			}
		}
	}
	return r
}

// Startable is true when nothing blocks the start. Narrowing issues still need
// the user's confirmation before Freeze; the caller records that confirmation.
func (r CompatReport) Startable() bool { return len(r.Blocking) == 0 }

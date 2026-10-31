package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/verdict"
)

var (
	ErrInvalid   = errors.New("REPORT_INVALID")
	ErrSensitive = errors.New("REPORT_CONTAINS_SENSITIVE_TEXT")
)

// Guard refuses text that must never reach a report (INV-07, SEC-06): absolute
// paths of either platform, UNC shares, file:// URLs, and any marker the caller
// registers (tests inject known sensitive values; production registers the
// source file names and the batch's own directories).
type Guard struct{ markers []string }

var pathLike = regexp.MustCompile(`(?i)(^|[\s"'=(:,\[])(/(Users|home|var|tmp|private|etc|Volumes|mnt|opt|root)/|[a-z]:[\\/]|\\\\[^\s\\]+\\|file://)`)

func NewGuard(markers ...string) Guard {
	var ms []string
	for _, m := range markers {
		if m != "" {
			ms = append(ms, m)
		}
	}
	return Guard{markers: ms}
}

// Check returns ErrSensitive for any string that looks like a path or contains a
// registered marker. The error never repeats the offending text.
func (g Guard) Check(ss ...string) error {
	for _, s := range ss {
		if !utf8.ValidString(s) || pathLike.MatchString(s) {
			return ErrSensitive
		}
		for _, m := range g.markers {
			if strings.Contains(s, m) {
				return ErrSensitive
			}
		}
	}
	return nil
}

// Stats is the INV-01 breakdown: who decided what.
type Stats struct {
	Auto               int     `json:"auto_resolved"`
	HumanItem          int     `json:"human_item"`
	HumanBulk          int     `json:"human_bulk"`
	Sampled            int     `json:"sampled"`
	BelowMinConfidence int     `json:"below_min_confidence"`
	MinConfidence      float64 `json:"min_confidence"`
}

// Coverage is the three-dimension summary of requirements 4.2.
type Coverage struct {
	Inspected            int      `json:"inspected"`
	Gaps                 int      `json:"gaps"`
	Retained             int      `json:"retained_by_decision"`
	Resolved             int      `json:"resolved_by_removal"`
	OutOfScope           int      `json:"out_of_scope"`
	OutOfScopeCategories []string `json:"out_of_scope_categories"`
}

// Validation is the recorded validation, with what it could not check.
type Validation struct {
	Status     string   `json:"status"`
	Conclusion string   `json:"conclusion"`
	Unverified []string `json:"unverified"` // scopes only
	Limits     []string `json:"limits"`     // stated limits, e.g. RASTER_OUTPUT_HAS_NO_TEXT_LAYER
}

// File is everything a single-file report says. It is built from neutral ids and
// counts; the builder is given no original value, source name or full path.
type File struct {
	ID                string // neutral identifier
	AppVersion        string
	Components        map[string]string // stage -> plugin id@version
	ModelVersion      string
	RuleSetVersion    string
	RulePackVersions  []string
	PolicyDigest      string
	Status            verdict.Status
	Stats             Stats
	Coverage          Coverage
	Validation        Validation
	AutoWidened       int
	ErrorCodes        []string
	RasterDPI         int
	FallbackFontPages []int
	SourceEncrypted   bool // "原文件已加密，输出未加密"
	NoSandbox         bool // SEC-07
}

type fileJSON struct {
	Schema            string            `json:"schema"`
	ID                string            `json:"id"`
	App               string            `json:"app_version"`
	Components        map[string]string `json:"components"`
	Model             string            `json:"model_version"`
	RuleSet           string            `json:"rule_set_version"`
	RulePacks         []string          `json:"rule_pack_versions"`
	Policy            string            `json:"policy_digest"`
	Status            string            `json:"status"`
	Stats             Stats             `json:"decisions"`
	Coverage          Coverage          `json:"coverage"`
	Validation        Validation        `json:"validation"`
	AutoWidened       int               `json:"auto_widened"`
	ErrorCodes        []string          `json:"error_codes"`
	RasterDPI         int               `json:"raster_dpi,omitempty"`
	FallbackFontPages []int             `json:"fallback_font_pages,omitempty"`
	Notices           []string          `json:"notices"`
}

// Notices that must accompany a report when they hold.
const (
	NoticeSourceEncrypted = "SOURCE_ENCRYPTED_OUTPUT_PLAIN"
	NoticeNoSandbox       = "OS_SANDBOX_NOT_ENABLED"
	NoticePartial         = "PARTIAL_RESULT_UNCHECKED_CONTENT_KEPT"
)

var neutral = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,127}$`)

func (f File) validate() error {
	if !neutral.MatchString(f.ID) || f.AppVersion == "" || f.Status == "" {
		return ErrInvalid
	}
	return nil
}

func (f File) json() fileJSON {
	var notices []string
	if f.SourceEncrypted {
		notices = append(notices, NoticeSourceEncrypted)
	}
	if f.NoSandbox {
		notices = append(notices, NoticeNoSandbox)
	}
	if f.Status == verdict.PartialResult || f.Coverage.Retained > 0 {
		notices = append(notices, NoticePartial)
	}
	sorted := func(s []string) []string { c := append([]string{}, s...); sort.Strings(c); return c }
	f.Validation.Unverified = sorted(f.Validation.Unverified)
	f.Validation.Limits = sorted(f.Validation.Limits)
	f.Coverage.OutOfScopeCategories = sorted(f.Coverage.OutOfScopeCategories)
	return fileJSON{Schema: "tacet.report/1", ID: f.ID, App: f.AppVersion, Components: f.Components, Model: f.ModelVersion, RuleSet: f.RuleSetVersion,
		RulePacks: sorted(f.RulePackVersions), Policy: f.PolicyDigest, Status: string(f.Status), Stats: f.Stats, Coverage: f.Coverage, Validation: f.Validation,
		AutoWidened: f.AutoWidened, ErrorCodes: sorted(f.ErrorCodes), RasterDPI: f.RasterDPI, FallbackFontPages: f.FallbackFontPages, Notices: notices}
}

// strings collects every string the report will contain, for the guard.
func (j fileJSON) strings() []string {
	out := []string{j.ID, j.App, j.Model, j.RuleSet, j.Policy, j.Status, j.Validation.Status, j.Validation.Conclusion}
	out = append(out, j.RulePacks...)
	out = append(out, j.ErrorCodes...)
	out = append(out, j.Validation.Unverified...)
	out = append(out, j.Validation.Limits...)
	out = append(out, j.Coverage.OutOfScopeCategories...)
	for k, v := range j.Components {
		out = append(out, k, v)
	}
	return out
}

// JSON renders the machine-readable report, deterministic and guarded.
func (f File) JSON(g Guard) ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	j := f.json()
	if err := g.Check(j.strings()...); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return nil, ErrInvalid
	}
	return append(b, '\n'), nil
}

// Summary renders the readable summary. It states what was and was not checked
// in the words of INV-01: kept gaps are never "inspected", below-threshold
// candidates are never "no sensitive information".
func (f File) Summary(g Guard) ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	j := f.json()
	if err := g.Check(j.strings()...); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Tacet report %s\nstatus: %s\n", j.ID, j.Status)
	fmt.Fprintf(&b, "decisions: automatic %d, one by one %d, in bulk %d, sampled %d\n", f.Stats.Auto, f.Stats.HumanItem, f.Stats.HumanBulk, f.Stats.Sampled)
	if f.Stats.MinConfidence > 0 {
		fmt.Fprintf(&b, "model minimum confidence %.2f: %d candidates below it were not handled (this is not a statement that they are not sensitive)\n", f.Stats.MinConfidence, f.Stats.BelowMinConfidence)
	}
	fmt.Fprintf(&b, "coverage: %d inspected, %d gaps (%d kept by decision and therefore still unchecked), %d resolved by removal, %d out of scope\n",
		f.Coverage.Inspected, f.Coverage.Gaps, f.Coverage.Retained, f.Coverage.Resolved, f.Coverage.OutOfScope)
	fmt.Fprintf(&b, "validation: %s (%s)\n", j.Validation.Status, j.Validation.Conclusion)
	for _, u := range j.Validation.Unverified {
		fmt.Fprintf(&b, "  not verified: %s\n", u)
	}
	for _, l := range j.Validation.Limits {
		fmt.Fprintf(&b, "  limit: %s\n", l)
	}
	if f.AutoWidened > 0 {
		fmt.Fprintf(&b, "automatic widening applied: %d\n", f.AutoWidened)
	}
	for _, n := range j.Notices {
		fmt.Fprintf(&b, "notice: %s\n", n)
	}
	for _, e := range j.ErrorCodes {
		fmt.Fprintf(&b, "error: %s\n", e)
	}
	return []byte(b.String()), nil
}

// Entry is one row of the delivery manifest.
type Entry struct {
	ID            string // neutral identifier
	OutputRelPath string // relative to the output location, never absolute
	OutputDigest  string
	Status        verdict.Status
	Partial       bool
}

type manifestJSON struct {
	Schema  string      `json:"schema"`
	Batch   string      `json:"batch"`
	Summary string      `json:"summary"`
	Files   []entryJSON `json:"files"`
}
type entryJSON struct {
	ID      string `json:"id"`
	Output  string `json:"output,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Status  string `json:"status"`
	Partial bool   `json:"partial,omitempty"`
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Manifest renders the delivery manifest. Entries are sorted by id; a file that
// has an output must name it relatively and carry its digest, one that has none
// must not.
func Manifest(batch string, entries []Entry, g Guard) ([]byte, error) {
	if !neutral.MatchString(batch) {
		return nil, ErrInvalid
	}
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	m := manifestJSON{Schema: "tacet.manifest/1", Batch: batch}
	statuses := make([]verdict.Status, 0, len(sorted))
	seen := map[string]bool{}
	for _, e := range sorted {
		if !neutral.MatchString(e.ID) || seen[e.ID] || e.Status == "" {
			return nil, ErrInvalid
		}
		seen[e.ID] = true
		delivered := e.Status == verdict.CompletedPerPolicy || e.Status == verdict.Delivered || e.Status == verdict.PartialResult
		if delivered {
			rel := e.OutputRelPath
			if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || strings.Contains(rel, "..") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(rel) || !digestRe.MatchString(e.OutputDigest) {
				return nil, ErrInvalid
			}
		} else if e.OutputRelPath != "" || e.OutputDigest != "" {
			return nil, ErrInvalid
		}
		m.Files = append(m.Files, entryJSON{ID: e.ID, Output: e.OutputRelPath, Digest: e.OutputDigest, Status: string(e.Status), Partial: e.Partial})
		statuses = append(statuses, e.Status)
	}
	s := verdict.Summarize(statuses)
	m.Summary = fmt.Sprintf("%d/%d delivered", s.Delivered, s.Total)
	// a relative output name is a path component, not a path: only the guard's markers apply to it
	for _, e := range sorted {
		if err := g.Check(e.ID, string(e.Status)); err != nil {
			return nil, err
		}
		if err := checkName(g, e.OutputRelPath); err != nil {
			return nil, err
		}
	}
	if err := g.Check(batch); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, ErrInvalid
	}
	return append(b, '\n'), nil
}

func checkName(g Guard, rel string) error {
	if rel == "" {
		return nil
	}
	for _, m := range g.markers {
		if strings.Contains(rel, m) {
			return ErrSensitive
		}
	}
	return nil
}

// ArtifactName is the neutral file name a report or manifest is written under.
func ArtifactName(kind, id string, content []byte) (string, error) {
	if !neutral.MatchString(id) || (kind != "report.json" && kind != "report.txt" && kind != "manifest.json") {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("tacet-%s-%s.%s", id, hex.EncodeToString(sum[:4]), kind), nil
}

// Write guards, names and writes the report pair through the publish module,
// the only writer of user-visible locations. Replaying the same report writes
// nothing new.
func Write(ctx context.Context, w publish.ArtifactWriter, f File, g Guard) (names [2]string, err error) {
	if w == nil {
		return names, ErrInvalid
	}
	j, err := f.JSON(g)
	if err != nil {
		return names, err
	}
	t, err := f.Summary(g)
	if err != nil {
		return names, err
	}
	for i, p := range []struct {
		kind string
		data []byte
	}{{"report.json", j}, {"report.txt", t}} {
		name, err := ArtifactName(p.kind, f.ID, p.data)
		if err != nil {
			return names, err
		}
		if err = w.WriteArtifact(ctx, name, p.data); err != nil {
			return names, err
		}
		names[i] = name
	}
	return names, nil
}

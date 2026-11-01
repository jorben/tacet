package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/store"
)

// Member is the app-owned record of one file: where it came from and what the
// pipeline learned about it. The database is encrypted at rest; none of this
// goes into events, reports or logs (INV-07).
type Member struct {
	Job       string
	Batch     string
	Source    string // absolute path
	Name      string // neutral output name
	Format    string // extension, lower case
	Input     string // resource id of the encrypted input copy
	Size      int64
	Fast      string // fast fingerprint of the original at preflight
	Detail    string // closed code of the last outcome (never content)
	Candidate string
	Unlocked  bool // Input is the decrypted copy of a password-protected original
}

type Run struct {
	Batch     string
	Preset    string
	OutputDir string
	Grant     bool
	Partial   bool
	Kind      string // quick or batch
	// Pending is the start the person asked for (JSON of StartRequest) while it has not taken effect: the
	// files were still being inspected when the call ended. Resume carries it out.
	Pending string
}

func appModule() store.Module {
	return store.Module{Name: "app", Prefix: "app_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE app_member(job TEXT PRIMARY KEY, batch TEXT NOT NULL, source TEXT NOT NULL, name TEXT NOT NULL, format TEXT NOT NULL,
			input TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, fast TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '', candidate TEXT NOT NULL DEFAULT '', unlocked INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX app_member_batch ON app_member(batch, job)`,
		`CREATE TABLE app_run(batch TEXT PRIMARY KEY, preset TEXT NOT NULL, output_dir TEXT NOT NULL, grant_publish INTEGER NOT NULL, partial INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL DEFAULT 'batch')`,
		`CREATE TABLE app_counter(name TEXT PRIMARY KEY, n INTEGER NOT NULL)`,
	}}, {Version: 2, SQL: []string{
		// a purged job keeps its neutral row (names, state) but no data
		`ALTER TABLE app_member ADD COLUMN purged INTEGER NOT NULL DEFAULT 0`,
	}}, {Version: 3, SQL: []string{
		// a start that was asked for but interrupted while the files were still being inspected
		`ALTER TABLE app_run ADD COLUMN pending TEXT NOT NULL DEFAULT ''`,
	}}}}
}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("app")
	return t, s, err
}

func hexSum(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// next returns the next number of a named counter, for neutral identifiers.
func next(tx store.Tx, name string) (int, error) {
	t, sc, err := scope(tx)
	if err != nil {
		return 0, err
	}
	if _, err = sc.Exec(`INSERT INTO app_counter(name,n) VALUES(?,1) ON CONFLICT(name) DO UPDATE SET n=n+1`, name); err != nil {
		return 0, err
	}
	var n int
	return n, t.QueryRow("SELECT n FROM app_counter WHERE name=?", name).Scan(&n)
}

func putMember(tx store.Tx, m Member) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	_, err = sc.Exec(`INSERT INTO app_member(job,batch,source,name,format,input,size,fast,detail,candidate,unlocked) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job) DO UPDATE SET input=excluded.input,size=excluded.size,fast=excluded.fast,detail=excluded.detail,candidate=excluded.candidate,unlocked=excluded.unlocked`,
		m.Job, m.Batch, m.Source, m.Name, m.Format, m.Input, m.Size, m.Fast, m.Detail, m.Candidate, m.Unlocked)
	return err
}

var ErrNoMember = errors.New("APP_MEMBER_NOT_FOUND")

func getMember(tx store.Tx, job string) (Member, error) {
	t, _, err := scope(tx)
	if err != nil {
		return Member{}, err
	}
	var m Member
	err = t.QueryRow(`SELECT job,batch,source,name,format,input,size,fast,detail,candidate,unlocked FROM app_member WHERE job=?`, job).
		Scan(&m.Job, &m.Batch, &m.Source, &m.Name, &m.Format, &m.Input, &m.Size, &m.Fast, &m.Detail, &m.Candidate, &m.Unlocked)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNoMember
	}
	return m, err
}

func putRun(tx store.Tx, r Run) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	_, err = sc.Exec(`INSERT INTO app_run(batch,preset,output_dir,grant_publish,partial,kind,pending) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(batch) DO UPDATE SET preset=excluded.preset,output_dir=excluded.output_dir,grant_publish=excluded.grant_publish,partial=excluded.partial,kind=excluded.kind,pending=excluded.pending`,
		r.Batch, r.Preset, r.OutputDir, b(r.Grant), b(r.Partial), r.Kind, r.Pending)
	return err
}

func getRun(tx store.Tx, batch string) (Run, error) {
	t, _, err := scope(tx)
	if err != nil {
		return Run{}, err
	}
	var r Run
	var g, p int
	err = t.QueryRow(`SELECT batch,preset,output_dir,grant_publish,partial,kind,pending FROM app_run WHERE batch=?`, batch).Scan(&r.Batch, &r.Preset, &r.OutputDir, &g, &p, &r.Kind, &r.Pending)
	r.Grant, r.Partial = g != 0, p != 0
	return r, err
}

// Built-in configuration identifiers.
const (
	RuleSetID = "builtin-cn-office"
	SchemeID  = "builtin-scheme-placeholder"
	PresetID  = "builtin-preset-office"
	// the model presets' ids are in modelKinds (catalog.go)
)

type pack struct {
	Rules []config.RuleContent `json:"rules"`
}

// seedConfig exposes one office preset. Historical preset objects remain addressable for saved tasks.
func seedConfig(tx store.Tx, repo *config.Repository, rulePack []byte, models []*modelInfo) error {
	var p pack
	if err := json.Unmarshal(rulePack, &p); err != nil || len(p.Rules) == 0 {
		return ErrOptions
	}
	rulesJSON, _ := json.Marshal(config.RulesContent{Rules: p.Rules})
	cats := map[string]string{}
	for _, r := range p.Rules {
		cats[r.Category] = "placeholder"
	}
	scheme, _ := json.Marshal(config.SchemeContent{CategoryActions: cats, ImageDefault: "remove"})
	preset, _ := json.Marshal(config.PresetContent{RuleSetID: RuleSetID, SchemeID: SchemeID, Mode: "guided", RulesOnly: true,
		MemoryBytes: 1 << 30, DPI: 200, Style: "default", RetentionDays: 0})
	type object struct {
		id, name string
		kind     config.Kind
		data     []byte
	}
	objects := []object{{RuleSetID, "办公文档规则", config.RuleSet, rulesJSON}, {SchemeID, "占位符方案", config.Scheme, scheme}, {PresetID, "办公外发（仅规则）", config.Preset, preset}}
	for _, model := range models {
		// The model finds persons and addresses that no rule can; rule categories keep their rules.
		k := model.kind
		mcats := map[string]string{}
		for c, a := range cats {
			mcats[c] = a
		}
		for _, c := range model.categories {
			mcats[c] = "placeholder"
		}
		mscheme, _ := json.Marshal(config.SchemeContent{CategoryActions: mcats, ImageDefault: "remove"})
		profile, _ := json.Marshal(config.ModelContent{ModelID: model.id, ManifestDigest: model.digest, Backend: model.backend, AdapterFloor: model.floor,
			Calibrated: model.recommended, LabelMap: model.labels, MemoryBytes: k.memory})
		mpreset, _ := json.Marshal(config.PresetContent{RuleSetID: RuleSetID, SchemeID: k.schemeID, ModelProfileID: k.profileID, Mode: "guided", RulesOnly: false,
			MinConfidence: model.recommended, MemoryBytes: k.presetMemory, DPI: 200, Style: "default", RetentionDays: 0})
		objects = append(objects, object{k.profileID, k.profileName, config.ModelProfile, profile}, object{k.schemeID, k.schemeName, config.Scheme, mscheme},
			object{k.presetID, k.preset, config.Preset, mpreset})
	}
	officeID := PresetID
	if len(models) > 0 {
		officeID = models[0].kind.presetID
	}
	for _, o := range objects {
		if o.id == officeID {
			o.name = "办公外发"
		}
		if _, err := repo.Get(tx, o.id); err == nil {
			// the rule set follows the shipped pack (e.g. rule names added later); task snapshots keep their copy
			if o.kind == config.ModelProfile || o.id == officeID || o.id == RuleSetID {
				if err := repo.RefreshBuiltin(tx, o.id, o.data, o.name); err != nil {
					return err
				}
			}
			continue
		}
		if _, err := repo.SeedBuiltin(tx, o.id, o.kind, o.name, o.data); err != nil {
			return err
		}
	}
	return nil
}

// The office preset stays bound to its default model even before that model is downloaded.
func (e *Engine) defaultPreset(_ context.Context) string {
	if len(e.models) > 0 {
		return e.models[0].kind.presetID
	}
	return PresetID
}

// legacyPreset hides historical variants while keeping their IDs usable by old saved tasks.
func (e *Engine) legacyPreset(id string) bool {
	if id == e.defaultPreset(context.Background()) {
		return false
	}
	if id == PresetID || id == ModelPresetID || id == "builtin-preset-privacy-filter" {
		return true
	}
	return false
}

func markPurged(tx store.Tx, job string) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	_, err = sc.Exec(`UPDATE app_member SET purged=1 WHERE job=?`, job)
	return err
}

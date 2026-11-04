package config

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrContent      = errors.New("CONFIG_CONTENT_INVALID")
	ErrMissing      = errors.New("CONFIG_NOT_FOUND")
	ErrConflict     = errors.New("VERSION_CONFLICT")
	ErrNameConflict = errors.New("CONFIG_NAME_CONFLICT")
	ErrReadOnly     = errors.New("CONFIG_READ_ONLY")
	ErrSchema       = errors.New("CONFIG_SCHEMA_UNSUPPORTED")
)

type Kind string

const (
	RuleSet      Kind = "ruleset"
	Scheme       Kind = "scheme"
	Preset       Kind = "preset"
	ModelProfile Kind = "model_profile"
	// Category is a user-defined category: its id is what rules report, its name is how it is shown, its
	// content the placeholder text (and color). Shipped and model-declared categories are not stored here.
	Category Kind = "category"
	// ModelMapping is the user's mapping of one model's labels to categories, replacing the default the
	// model profile carries (from the model's manifest). Its name is the model id: one per model.
	ModelMapping Kind = "model_mapping"
)

func validKind(kind Kind) bool {
	return kind == RuleSet || kind == Scheme || kind == Preset || kind == ModelProfile || kind == Category || kind == ModelMapping
}

// GetByName returns the object of this kind with this name (names are unique per kind).
func (r *Repository) GetByName(tx store.Tx, kind Kind, name string) (Object, bool, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Object{}, false, err
	}
	var id string
	switch err = t.QueryRow("SELECT id FROM config_object WHERE kind=? AND name=?", string(kind), name).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		return Object{}, false, nil
	case err != nil:
		return Object{}, false, err
	}
	o, err := r.Get(tx, id)
	return o, err == nil, err
}

func Module() store.Module {
	return store.Module{Name: "config", Prefix: "config_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE config_object(id TEXT PRIMARY KEY,kind TEXT NOT NULL,name TEXT NOT NULL,note TEXT NOT NULL,version INTEGER NOT NULL,digest TEXT NOT NULL,content BLOB NOT NULL,readonly INTEGER NOT NULL,UNIQUE(kind,name))`,
		`CREATE TABLE config_history(id TEXT NOT NULL,version INTEGER NOT NULL,digest TEXT NOT NULL,content BLOB NOT NULL,saved_ns INTEGER NOT NULL,PRIMARY KEY(id,version))`,
	}}, {Version: 2, SQL: []string{
		`CREATE TABLE config_settings(key TEXT PRIMARY KEY,value BLOB NOT NULL)`,
	}}}}
}

// Validate is required at wiring time and checks the kind-specific schema,
// references and rule compilation budget using this same transaction. Persistence
// is not a substitute for validation; nil validators fail closed on writes.
type Validate func(store.Tx, Kind, json.RawMessage) error

type Repository struct {
	validate Validate
	now      func() time.Time
}

func NewRepository(validate Validate) *Repository {
	return &Repository{validate: validate, now: time.Now}
}

type Object struct {
	ID                 string
	Kind               Kind
	Name, Note         string
	Version            uint64
	Digest             string
	Content            json.RawMessage
	ReadOnly           bool
	MinConfidenceReset bool // transient result metadata, not part of stored content
}

type Version struct {
	Number  uint64
	Digest  string
	SavedAt time.Time
}

func transaction(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	scope, err := t.Module("config")
	return t, scope, err
}

func (r *Repository) Get(tx store.Tx, id string) (Object, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Object{}, err
	}
	var object Object
	err = t.QueryRow("SELECT id,kind,name,note,version,digest,content,readonly FROM config_object WHERE id=?", id).Scan(&object.ID, &object.Kind, &object.Name, &object.Note, &object.Version, &object.Digest, &object.Content, &object.ReadOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return Object{}, ErrMissing
	}
	if err != nil {
		return Object{}, err
	}
	return object, nil
}

func metadata(id, name, note string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(name) && utf8.ValidString(note) && strings.TrimSpace(name) != "" && len(name) <= 512 && len(note) <= 8192
}

func contentDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func (r *Repository) validateContent(tx store.Tx, kind Kind, input []byte) ([]byte, error) {
	if !validKind(kind) || r == nil || r.validate == nil {
		return nil, ErrContent
	}
	canonical, err := Canonical(input)
	if err != nil {
		return nil, err
	}
	if err = r.validate(tx, kind, canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

func nameAvailable(t *store.Txn, kind Kind, name, id string) error {
	var conflict string
	err := t.QueryRow("SELECT id FROM config_object WHERE kind=? AND name=? AND id<>?", string(kind), name, id).Scan(&conflict)
	if err == nil {
		return ErrNameConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func emit(tx store.Tx, o Object) {
	tx.Emit(store.Event{Type: "ConfigSaved", Subject: kernel.ArtifactRef{Kind: kernel.ArtifactKind(o.Kind), ID: o.ID, Version: o.Version, Digest: o.Digest}})
}

// Create starts an independent user object at v1. It does not accept a read-only
// flag from the user; bundled objects enter only through SeedBuiltin.
func (r *Repository) Create(tx store.Tx, id string, kind Kind, name, note string, input []byte) (Object, error) {
	return r.create(tx, id, kind, name, note, input, false)
}
func (r *Repository) create(tx store.Tx, id string, kind Kind, name, note string, input []byte, readonly bool) (Object, error) {
	if !metadata(id, name, note) {
		return Object{}, ErrContent
	}
	canonical, err := r.validateContent(tx, kind, input)
	if err != nil {
		return Object{}, err
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return Object{}, err
	}
	var exists int
	if err = t.QueryRow("SELECT COUNT(*) FROM config_object WHERE id=?", id).Scan(&exists); err != nil {
		return Object{}, err
	}
	if exists > 0 {
		return Object{}, ErrConflict
	}
	if err = nameAvailable(t, kind, name, id); err != nil {
		return Object{}, err
	}
	object := Object{ID: id, Kind: kind, Name: name, Note: note, Version: 1, Digest: contentDigest(canonical), Content: canonical, ReadOnly: readonly}
	if _, err = scope.Exec("INSERT INTO config_object(id,kind,name,note,version,digest,content,readonly) VALUES(?,?,?,?,?,?,?,?)", id, string(kind), name, note, 1, object.Digest, canonical, readonly); err != nil {
		return Object{}, err
	}
	if !readonly {
		if _, err = scope.Exec("INSERT INTO config_history(id,version,digest,content,saved_ns) VALUES(?,?,?,?,?)", id, 1, object.Digest, canonical, r.now().UnixNano()); err != nil {
			return Object{}, err
		}
	}
	emit(tx, object)
	return object, nil
}

// SeedBuiltin is for signed application/installed-rulepack wiring only; these
// objects have no user-editable history. Version provenance remains owned by the
// application/plugin catalog, outside this generic storage primitive.
func (r *Repository) SeedBuiltin(tx store.Tx, id string, kind Kind, name string, input []byte) (Object, error) {
	return r.create(tx, id, kind, name, "", input, true)
}

// RefreshBuiltin updates application-owned content when the signed catalog changes.
// Existing task snapshots retain their own immutable copy.
func (r *Repository) RefreshBuiltin(tx store.Tx, id string, input []byte, name ...string) error {
	o, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if !o.ReadOnly {
		return ErrReadOnly
	}
	canonical, err := r.validateContent(tx, o.Kind, input)
	if err != nil {
		return err
	}
	digest := contentDigest(canonical)
	if len(name) > 0 && metadata(id, name[0], o.Note) {
		o.Name = name[0]
	}
	_, sc, err := transaction(tx)
	if err != nil {
		return err
	}
	if digest == o.Digest {
		_, err = sc.Exec("UPDATE config_object SET name=? WHERE id=? AND readonly=1", o.Name, id)
		return err
	}
	if _, err = sc.Exec("UPDATE config_object SET version=version+1,digest=?,content=?,name=? WHERE id=? AND readonly=1", digest, canonical, o.Name, id); err != nil {
		return err
	}
	o.Version++
	o.Digest, o.Content = digest, canonical
	emit(tx, o)
	return nil
}

// Save compares expectedVersion before updating metadata. Canonical content
// unchanged means no new version and no new history timestamp. Names and notes
// never participate in the digest. Caller wraps all work in one transaction.
func (r *Repository) Save(tx store.Tx, id string, expectedVersion uint64, input []byte, name, note string) (Object, error) {
	if !metadata(id, name, note) {
		return Object{}, ErrContent
	}
	object, err := r.Get(tx, id)
	if err != nil {
		return Object{}, err
	}
	if object.ReadOnly {
		return Object{}, ErrReadOnly
	}
	if object.Version != expectedVersion {
		return Object{}, ErrConflict
	}
	canonical, err := Canonical(input)
	if err != nil {
		return Object{}, err
	}
	if object.Kind == Preset {
		var reset bool
		canonical, reset, err = resetModelThreshold(tx, object.Content, canonical)
		if err != nil {
			return Object{}, err
		}
		object.MinConfidenceReset = reset
	}
	canonical, err = r.validateContent(tx, object.Kind, canonical)
	if err != nil {
		return Object{}, err
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return Object{}, err
	}
	if err = nameAvailable(t, object.Kind, name, id); err != nil {
		return Object{}, err
	}
	digest := contentDigest(canonical)
	changed := digest != object.Digest
	if changed {
		object.Version++
	}
	if object.Version > 9007199254740991 {
		return Object{}, ErrContent
	}
	object.Name, object.Note, object.Content, object.Digest = name, note, canonical, digest
	result, err := scope.Exec("UPDATE config_object SET name=?,note=?,version=?,digest=?,content=? WHERE id=? AND version=? AND readonly=0", name, note, object.Version, digest, canonical, id, expectedVersion)
	if err != nil {
		return Object{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Object{}, err
	}
	if count != 1 {
		return Object{}, ErrConflict
	}
	if changed {
		if _, err = scope.Exec("INSERT INTO config_history(id,version,digest,content,saved_ns) VALUES(?,?,?,?,?)", id, object.Version, digest, canonical, r.now().UnixNano()); err != nil {
			return Object{}, err
		}
		if _, err = scope.Exec("DELETE FROM config_history WHERE id=? AND version<=?", id, int64(object.Version)-5); err != nil {
			return Object{}, err
		}
	}
	emit(tx, object)
	return object, nil
}

func (r *Repository) History(tx store.Tx, id string) ([]Version, error) {
	if _, err := r.Get(tx, id); err != nil {
		return nil, err
	}
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT version,digest,saved_ns FROM config_history WHERE id=? ORDER BY version DESC LIMIT 5", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []Version
	for rows.Next() {
		var v Version
		var saved int64
		if err = rows.Scan(&v.Number, &v.Digest, &saved); err != nil {
			return nil, err
		}
		v.SavedAt = time.Unix(0, saved)
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// Content returns the stored content of one kept version of an object.
func (r *Repository) Content(tx store.Tx, id string, version uint64) ([]byte, error) {
	if _, err := r.Get(tx, id); err != nil {
		return nil, err
	}
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	var content []byte
	if err = t.QueryRow("SELECT content FROM config_history WHERE id=? AND version=?", id, version).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMissing
		}
		return nil, err
	}
	return content, nil
}

func (r *Repository) Rollback(tx store.Tx, id string, expectedVersion, restoreVersion uint64) (Object, error) {
	object, err := r.Get(tx, id)
	if err != nil {
		return Object{}, err
	}
	if object.ReadOnly {
		return Object{}, ErrReadOnly
	}
	if object.Version != expectedVersion {
		return Object{}, ErrConflict
	}
	t, _, err := transaction(tx)
	if err != nil {
		return Object{}, err
	}
	var content []byte
	if err = t.QueryRow("SELECT content FROM config_history WHERE id=? AND version=?", id, restoreVersion).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Object{}, ErrMissing
		}
		return Object{}, err
	}
	return r.Save(tx, id, expectedVersion, content, object.Name, object.Note)
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", ErrContent
	}
	return "cfg_" + hex.EncodeToString(data[:]), nil
}

// Copy detaches a user object from any builtin/rulepack provenance and starts v1.
func (r *Repository) Copy(tx store.Tx, id, name string) (Object, error) {
	original, err := r.Get(tx, id)
	if err != nil {
		return Object{}, err
	}
	copyID, err := newID()
	if err != nil {
		return Object{}, err
	}
	return r.Create(tx, copyID, original.Kind, name, original.Note, original.Content)
}

// Package is schema-versioned exchange data, with only current content, no
// history, runtime model weights, ids or task grants. Sensitive export policy
// must be checked separately for each channel (CLI policy bundles are stricter).
type Package struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          Kind            `json:"kind"`
	Name          string          `json:"name"`
	Note          string          `json:"note"`
	Content       json.RawMessage `json:"content"`
}

// List returns a bounded page by immutable object id; histories/content are
// fetched only for the selected object, not every item in the side list.
type Summary struct {
	ID         string
	Kind       Kind
	Name, Note string
	Version    uint64
	ReadOnly   bool
}

func (r *Repository) List(tx store.Tx, kind Kind, afterID string, limit int) ([]Summary, error) {
	if !validKind(kind) || limit <= 0 || limit > 200 {
		return nil, ErrContent
	}
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT id,kind,name,note,version,readonly FROM config_object WHERE kind=? AND id>? ORDER BY id LIMIT ?", string(kind), afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var item Summary
		if err = rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Note, &item.Version, &item.ReadOnly); err != nil {
			return nil, err
		}
		summaries = append(summaries, item)
	}
	return summaries, rows.Err()
}

// Settings have no content-history versions. Automatic update checking remains
// false until the user opts in; terms of settings presentation stay in GUI.
type Settings struct {
	CloseBehavior         string `json:"close_behavior"`
	Theme                 string `json:"theme"`
	DefaultStyle          string `json:"default_style"`
	AutomaticUpdateChecks bool   `json:"automatic_update_checks"`
	// What the settings page also keeps. Empty or zero in data written before they existed means the default.
	RedactStyle   string `json:"redact_style,omitempty"`   // look of the covering blocks in the screens
	ResourceTier  string `json:"resource_tier,omitempty"`  // comfort or std
	RetentionDays int    `json:"retention_days,omitempty"` // how long unfinished work is kept: 0, 7 or 30
	RetentionSet  bool   `json:"retention_set,omitempty"`  // distinguishes "do not keep" (0) from "never chosen"
	// GPUAcceleration prefers a supported GPU provider; CPU fallback uses the same model files.
	GPUAcceleration bool `json:"gpu_acceleration"`
}

var redactStyles = map[string]bool{"black": true, "gray": true, "mosaic": true, "stripe": true, "heart": true, "paw": true, "snow": true, "seal": true, "star": true, "dot": true, "rainbow": true, "plaid": true}

// withDefaults fills what older data lacks.
func (s Settings) withDefaults() Settings {
	if s.RedactStyle == "" {
		s.RedactStyle = "black"
	}
	if s.ResourceTier == "" {
		s.ResourceTier = "comfort"
	}
	if !s.RetentionSet {
		s.RetentionDays, s.RetentionSet = 7, true
	}
	return s
}

func DefaultSettings() Settings {
	return Settings{CloseBehavior: "tray", Theme: "system", DefaultStyle: "solid", GPUAcceleration: true}.withDefaults()
}
func (r *Repository) Settings(tx store.Tx) (Settings, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Settings{}, err
	}
	var data []byte
	if err = t.QueryRow("SELECT value FROM config_settings WHERE key='application'").Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	} else if err != nil {
		return Settings{}, err
	}
	settings := Settings{GPUAcceleration: true} // missing GPU preference means enabled; an explicit false is preserved
	if json.Unmarshal(data, &settings) != nil {
		return Settings{}, ErrContent
	}
	settings = settings.withDefaults()
	if !validSettings(settings) {
		return Settings{}, ErrContent
	}
	return settings, nil
}
func validSettings(s Settings) bool {
	return (s.CloseBehavior == "tray" || s.CloseBehavior == "quit") && (s.Theme == "system" || s.Theme == "light" || s.Theme == "dark") && (s.DefaultStyle == "solid" || s.DefaultStyle == "diagonal" || s.DefaultStyle == "mosaic" || s.DefaultStyle == "pattern") &&
		redactStyles[s.RedactStyle] && (s.ResourceTier == "comfort" || s.ResourceTier == "std") && (s.RetentionDays == 0 || s.RetentionDays == 7 || s.RetentionDays == 30)
}
func (r *Repository) SaveSettings(tx store.Tx, s Settings) error {
	s.RetentionSet = true
	if !validSettings(s) {
		return ErrContent
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return ErrContent
	}
	if _, err = scope.Exec("INSERT INTO config_settings(key,value) VALUES('application',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", data); err != nil {
		return err
	}
	tx.Emit(store.Event{Type: "ConfigSaved", Subject: kernel.ArtifactRef{Kind: "settings", ID: "application", Version: 1}})
	return nil
}

func (r *Repository) Import(tx store.Tx, p Package) (Object, error) {
	if p.SchemaVersion != 1 {
		return Object{}, ErrSchema
	}
	id, err := newID()
	if err != nil {
		return Object{}, err
	}
	return r.Create(tx, id, p.Kind, p.Name, p.Note, p.Content)
}

// reportedByRule reports whether a rule of any rule set reports the category. Such a category cannot be
// deleted: the rule would report a category that no longer exists.
func reportedByRule(t *store.Txn, id string) (bool, error) {
	rows, err := t.Query("SELECT content FROM config_object WHERE kind=?", string(RuleSet))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var content []byte
		if err = rows.Scan(&content); err != nil {
			return false, err
		}
		var rs RulesContent
		if json.Unmarshal(content, &rs) != nil {
			continue
		}
		for _, rule := range rs.Rules {
			if rule.Category == id {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

// userObjects lists the ids of the user's objects of a kind (built-in ones are read-only).
func userObjects(t *store.Txn, kind Kind) ([]string, error) {
	rows, err := t.Query("SELECT id FROM config_object WHERE kind=? AND readonly=0 ORDER BY id", string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// withoutCategory is the content of a model mapping or scheme with category id taken out: the labels that
// count as it are no longer used, a scheme holds no setting for it. Members the category is not part of are
// kept as stored. changed is false when the object does not refer to it.
func withoutCategory(kind Kind, content []byte, id string) (out []byte, changed bool, err error) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(content, &doc) != nil {
		return nil, false, ErrContent
	}
	switch kind {
	case ModelMapping:
		var labels map[string]string
		if json.Unmarshal(doc["label_map"], &labels) != nil {
			return nil, false, ErrContent
		}
		for label, category := range labels {
			if category == id {
				delete(labels, label)
				changed = true
			}
		}
		if !changed {
			return content, false, nil
		}
		if len(labels) == 0 {
			return nil, false, ErrInUse // the mapping would count none of the model's labels
		}
		doc["label_map"], err = json.Marshal(labels)
	case Scheme:
		var actions map[string]string
		if json.Unmarshal(doc["category_actions"], &actions) != nil {
			return nil, false, ErrContent
		}
		if _, ok := actions[id]; !ok {
			return content, false, nil
		}
		delete(actions, id)
		changed = true
		if doc["category_actions"], err = json.Marshal(actions); err != nil {
			return nil, false, err
		}
		if raw, ok := doc["human_review"]; ok {
			var review []string
			if json.Unmarshal(raw, &review) != nil {
				return nil, false, ErrContent
			}
			kept := []string{}
			for _, c := range review {
				if c != id {
					kept = append(kept, c)
				}
			}
			doc["human_review"], err = json.Marshal(kept)
		}
	default:
		return content, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out, err = json.Marshal(doc)
	return out, changed, err
}

// detachCategory takes a category that is being deleted out of the model mappings and schemes that refer
// to it, each changed object saved as a new version of its own. A mapping whose labels all count as the
// category would be left with none, so the category stays (ErrInUse). Task snapshots hold their content
// and are not touched.
func (r *Repository) detachCategory(tx store.Tx, t *store.Txn, id string) error {
	for _, kind := range []Kind{ModelMapping, Scheme} {
		ids, err := userObjects(t, kind)
		if err != nil {
			return err
		}
		for _, oid := range ids {
			o, err := r.Get(tx, oid)
			if err != nil {
				return err
			}
			content, changed, err := withoutCategory(kind, o.Content, id)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if _, err = r.Save(tx, o.ID, o.Version, content, o.Name, o.Note); err != nil {
				return err
			}
		}
	}
	return nil
}

// ErrInUse says a preset still refers to the object, or a rule reports the category.
var ErrInUse = errors.New("CONFIG_IN_USE")

// Delete removes a user object and its history. Built-in objects stay, and so does an object a preset
// still refers to: deleting it would leave that preset unable to start. A category a rule reports stays
// too; one that model mappings or schemes refer to is taken out of them first (detachCategory). Tasks already running keep their
// frozen copy of the configuration (the snapshot holds content, not references).
func (r *Repository) Delete(tx store.Tx, id string, expectedVersion uint64) error {
	object, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if object.ReadOnly {
		return ErrReadOnly
	}
	if object.Version != expectedVersion {
		return ErrConflict
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	if object.Kind == Category {
		// A rule that reports the category keeps it. Model mappings and schemes are shared configuration that
		// only refers to it: they let go of it. The screen's dialog lists only the affected models one by one;
		// the schemes' settings for the category are cleared too, which its subtitle states only in general.
		if used, err := reportedByRule(t, id); err != nil || used {
			if err == nil {
				err = ErrInUse
			}
			return err
		}
		if err = r.detachCategory(tx, t, id); err != nil {
			return err
		}
	}
	if object.Kind != Preset && object.Kind != Category && object.Kind != ModelMapping {
		rows, err := t.Query("SELECT content FROM config_object WHERE kind=?", string(Preset))
		if err != nil {
			return err
		}
		var used bool
		for rows.Next() {
			var content []byte
			if err = rows.Scan(&content); err != nil {
				rows.Close()
				return err
			}
			var p PresetContent
			if json.Unmarshal(content, &p) != nil {
				continue
			}
			refs := append([]string{p.RuleSetID, p.SchemeID, p.ModelProfileID}, p.ExtraRuleSetIDs...)
			for _, ref := range refs {
				if ref == id {
					used = true
				}
			}
		}
		rows.Close()
		if used {
			return ErrInUse
		}
	}
	if _, err = scope.Exec("DELETE FROM config_history WHERE id=?", id); err != nil {
		return err
	}
	if _, err = scope.Exec("DELETE FROM config_object WHERE id=?", id); err != nil {
		return err
	}
	tx.Emit(store.Event{Type: "ConfigDeleted", Subject: kernel.ArtifactRef{Kind: kernel.ArtifactKind(object.Kind), ID: id, Version: object.Version, Digest: object.Digest}})
	return nil
}

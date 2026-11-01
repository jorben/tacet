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
)

func validKind(kind Kind) bool {
	return kind == RuleSet || kind == Scheme || kind == Preset || kind == ModelProfile
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
	// GPUAcceleration: batches started from now on run a model's accelerated build when it is downloaded.
	GPUAcceleration bool `json:"gpu_acceleration,omitempty"`
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
	return Settings{CloseBehavior: "tray", Theme: "system", DefaultStyle: "solid"}.withDefaults()
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
	var settings Settings
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

// ErrInUse says a preset still refers to the object.
var ErrInUse = errors.New("CONFIG_IN_USE")

// Delete removes a user object and its history. Built-in objects stay, and so does an object a preset
// still refers to: deleting it would leave that preset unable to start. Tasks already running keep their
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
	if object.Kind != Preset {
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

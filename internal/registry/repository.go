package registry

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrInvalid    = errors.New("PLUGIN_MANIFEST_INVALID")
	ErrMissing    = errors.New("PLUGIN_NOT_FOUND")
	ErrConflict   = errors.New("PLUGIN_VERSION_CONFLICT")
	ErrRuntime    = errors.New("PLUGIN_RUNTIME_UNSUPPORTED")
	ErrProtocol   = errors.New("PLUGIN_PROTOCOL_UNSUPPORTED")
	ErrPlatform   = errors.New("PLUGIN_PLATFORM_UNSUPPORTED")
	ErrSandbox    = errors.New("PLUGIN_SANDBOX_UNAVAILABLE")
	ErrReferenced = errors.New("PLUGIN_VERSION_REFERENCED")
	ErrNotReady   = errors.New("PLUGIN_NOT_READY")
	ErrReadOnly   = errors.New("BUILTIN_PLUGIN_READ_ONLY")
)

func Module() store.Module {
	return store.Module{Name: "registry", Prefix: "registry_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE registry_plugin(id TEXT NOT NULL,version TEXT NOT NULL,digest TEXT NOT NULL,env TEXT NOT NULL,builtin INTEGER NOT NULL,enabled INTEGER NOT NULL,manifest BLOB NOT NULL,PRIMARY KEY(id,version,digest))`,
		`CREATE TABLE registry_default(format TEXT PRIMARY KEY,id TEXT NOT NULL,version TEXT NOT NULL,digest TEXT NOT NULL)`,
		`CREATE TABLE registry_grant(id TEXT NOT NULL,version TEXT NOT NULL,digest TEXT NOT NULL,PRIMARY KEY(id,version,digest))`,
		`CREATE TABLE registry_reference(workspace TEXT NOT NULL,owner TEXT NOT NULL,kind TEXT NOT NULL,resource TEXT NOT NULL,version TEXT NOT NULL,digest TEXT NOT NULL,PRIMARY KEY(workspace,owner,kind,resource,version,digest))`,
		`CREATE INDEX registry_referenced_resource ON registry_reference(kind,resource,version,digest)`,
	}}}}
}

type Identity struct{ ID, Version, Digest string }
type Manifest struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Kind     string `json:"kind"`
	License  string `json:"license"`
	Protocol struct {
		Min string `json:"min"`
		Max string `json:"max"`
	} `json:"protocol"`
	Roles   []string `json:"roles"`
	Runtime *struct {
		Kind  string `json:"kind"`
		Entry string `json:"entry"`
	} `json:"runtime"`
	Platforms    []string `json:"platforms"`
	Formats      []string `json:"formats"`
	Capabilities map[string]struct {
		Supported bool   `json:"supported"`
		Notes     string `json:"notes"`
	} `json:"capabilities"`
	ScratchPlaintext bool `json:"scratch_plaintext"`
}

type Plugin struct {
	Identity
	Environment      string
	Builtin, Enabled bool
	Manifest         Manifest
}

type Repository struct {
	schema  *jsonschema.Schema
	target  string
	sandbox func() bool
}
type denyRemote struct{}

func (denyRemote) Load(string) (any, error) { return nil, ErrInvalid }

func NewRepository(target string, sandbox func() bool) (*Repository, error) {
	if target != "darwin-arm64" && target != "windows-amd64" {
		return nil, ErrPlatform
	}
	data, err := protocol.Schemas.ReadFile("schema/v1/manifest.schema.json")
	if err != nil {
		return nil, ErrInvalid
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(denyRemote{})
	const id = "https://github.com/jorben/tacet/protocol/schema/v1/manifest.schema.json"
	if err = compiler.AddResource(id, value); err != nil {
		return nil, ErrInvalid
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Repository{schema: schema, target: target, sandbox: sandbox}, nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var modulePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)*$`)

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (r *Repository) manifest(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > 1024*1024 {
		return Manifest{}, ErrInvalid
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil || r.schema.Validate(value) != nil {
		return Manifest{}, ErrInvalid
	}
	var manifest Manifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version == "" || len(manifest.Version) > 128 || manifest.License == "" {
		return Manifest{}, ErrInvalid
	}
	if manifest.Protocol.Min != "1.0" || manifest.Protocol.Max != "1.0" {
		return Manifest{}, ErrProtocol
	}
	if manifest.Kind == "plugin" {
		if manifest.Runtime == nil || manifest.Runtime.Kind != "python" {
			return Manifest{}, ErrRuntime
		}
		if !modulePattern.MatchString(manifest.Runtime.Entry) {
			return Manifest{}, ErrInvalid
		}
		if !contains(manifest.Platforms, r.target) {
			return Manifest{}, ErrPlatform
		}
	}
	return manifest, nil
}

// Check validates a manifest exactly as Register would, without storing anything,
// so installation can refuse a package that could never be registered before it
// spends a deployment on it.
func (r *Repository) Check(data []byte) error {
	_, err := r.manifest(data)
	return err
}

func transaction(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("registry")
	return t, s, err
}
func changed(tx store.Tx, p Plugin) {
	tx.Emit(store.Event{Type: "PluginChanged", Subject: kernel.ArtifactRef{Kind: "plugin", ID: p.ID, Digest: p.Digest}})
}

// Register is called only after source/lock/environment integrity and worker
// readiness checks. It records a fixed package digest and never auto-enables or
// changes defaults. Third-party code cannot even be registered on a platform
// whose sandbox acceptance is unavailable (FR-45); rulesets need no code sandbox.
func (r *Repository) Register(tx store.Tx, data []byte, digest, environment string, builtin bool) (Plugin, error) {
	manifest, err := r.manifest(data)
	if err != nil {
		return Plugin{}, err
	}
	if !digestPattern.MatchString(digest) {
		return Plugin{}, ErrInvalid
	}
	if manifest.Kind == "plugin" {
		if !digestPattern.MatchString(environment) {
			return Plugin{}, ErrNotReady
		}
		if !builtin && (r.sandbox == nil || !r.sandbox()) {
			return Plugin{}, ErrSandbox
		}
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return Plugin{}, err
	}
	var exists int
	if err = t.QueryRow("SELECT COUNT(*) FROM registry_plugin WHERE id=? AND version=? AND digest=?", manifest.ID, manifest.Version, digest).Scan(&exists); err != nil {
		return Plugin{}, err
	}
	if exists > 0 {
		return Plugin{}, ErrConflict
	}
	p := Plugin{Identity: Identity{manifest.ID, manifest.Version, digest}, Environment: environment, Builtin: builtin, Manifest: manifest}
	if _, err = scope.Exec("INSERT INTO registry_plugin(id,version,digest,env,builtin,enabled,manifest) VALUES(?,?,?,?,?,0,?)", p.ID, p.Version, p.Digest, environment, builtin, data); err != nil {
		return Plugin{}, err
	}
	changed(tx, p)
	return p, nil
}

func (r *Repository) Get(tx store.Tx, id Identity) (Plugin, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Plugin{}, err
	}
	p := Plugin{Identity: id}
	var data []byte
	err = t.QueryRow("SELECT env,builtin,enabled,manifest FROM registry_plugin WHERE id=? AND version=? AND digest=?", id.ID, id.Version, id.Digest).Scan(&p.Environment, &p.Builtin, &p.Enabled, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return Plugin{}, ErrMissing
	}
	if err != nil {
		return Plugin{}, err
	}
	if json.Unmarshal(data, &p.Manifest) != nil {
		return Plugin{}, ErrInvalid
	}
	return p, nil
}

func (r *Repository) Enable(tx store.Tx, id Identity, enabled bool) error {
	p, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if enabled && p.Manifest.Kind == "plugin" && !p.Builtin && (r.sandbox == nil || !r.sandbox()) {
		return ErrSandbox
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	if _, err = scope.Exec("UPDATE registry_plugin SET enabled=? WHERE id=? AND version=? AND digest=?", enabled, id.ID, id.Version, id.Digest); err != nil {
		return err
	}
	changed(tx, p)
	return nil
}

// SetDefault is explicit. Enabling/installing an alternative plugin never calls
// this implicitly; frozen tasks keep their exact old Identity and environment.
func (r *Repository) SetDefault(tx store.Tx, format string, id Identity) error {
	p, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if !p.Enabled || !contains(p.Manifest.Formats, format) || !contains(p.Manifest.Roles, "format") {
		return ErrNotReady
	}
	if p.Manifest.Kind == "plugin" && !p.Builtin && (r.sandbox == nil || !r.sandbox()) {
		return ErrSandbox
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	if _, err = scope.Exec("INSERT INTO registry_default(format,id,version,digest) VALUES(?,?,?,?) ON CONFLICT(format) DO UPDATE SET id=excluded.id,version=excluded.version,digest=excluded.digest", format, id.ID, id.Version, id.Digest); err != nil {
		return err
	}
	changed(tx, p)
	return nil
}
func (r *Repository) Default(tx store.Tx, format string) (Plugin, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return Plugin{}, err
	}
	var id Identity
	if err = t.QueryRow("SELECT id,version,digest FROM registry_default WHERE format=?", format).Scan(&id.ID, &id.Version, &id.Digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plugin{}, ErrMissing
		}
		return Plugin{}, err
	}
	p, err := r.Get(tx, id)
	if err != nil {
		return Plugin{}, err
	}
	if !p.Enabled {
		return Plugin{}, ErrNotReady
	}
	if !p.Builtin && p.Manifest.Kind == "plugin" && (r.sandbox == nil || !r.sandbox()) {
		return Plugin{}, ErrSandbox
	}
	return p, nil
}

func (r *Repository) GrantCompletion(tx store.Tx, id Identity) error {
	p, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if !contains(p.Manifest.Roles, "format") {
		return ErrInvalid
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	if _, err = scope.Exec("INSERT OR IGNORE INTO registry_grant(id,version,digest) VALUES(?,?,?)", id.ID, id.Version, id.Digest); err != nil {
		return err
	}
	changed(tx, p)
	return nil
}
func (r *Repository) CompletionGranted(tx store.Tx, id Identity) (bool, error) {
	p, err := r.Get(tx, id)
	if err != nil {
		return false, err
	}
	if p.Builtin {
		return true, nil
	}
	t, _, err := transaction(tx)
	if err != nil {
		return false, err
	}
	var count int
	err = t.QueryRow("SELECT COUNT(*) FROM registry_grant WHERE id=? AND version=? AND digest=?", id.ID, id.Version, id.Digest).Scan(&count)
	return count > 0, err
}

// Reference pins a fixed resource for a task/snapshot in one workspace. Other
// resource types use the same index, so cleanup can retain a shared environment
// or model while any task in either GUI/CLI workspace still depends on it.
type Reference struct{ Workspace, Owner, Kind, Resource, Version, Digest string }

func (r *Repository) Pin(tx store.Tx, ref Reference) error {
	if ref.Workspace == "" || ref.Owner == "" || ref.Resource == "" || ref.Version == "" || !digestPattern.MatchString(ref.Digest) {
		return ErrInvalid
	}
	switch ref.Kind {
	case "plugin", "ruleset", "environment", "model":
	default:
		return ErrInvalid
	}
	if ref.Kind == "plugin" || ref.Kind == "ruleset" {
		if _, err := r.Get(tx, Identity{ref.Resource, ref.Version, ref.Digest}); err != nil {
			return err
		}
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	_, err = scope.Exec("INSERT OR IGNORE INTO registry_reference(workspace,owner,kind,resource,version,digest) VALUES(?,?,?,?,?,?)", ref.Workspace, ref.Owner, ref.Kind, ref.Resource, ref.Version, ref.Digest)
	return err
}
func (r *Repository) Unpin(tx store.Tx, workspace, owner string) error {
	if workspace == "" || owner == "" {
		return ErrInvalid
	}
	_, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	_, err = scope.Exec("DELETE FROM registry_reference WHERE workspace=? AND owner=?", workspace, owner)
	return err
}

func (r *Repository) Remove(tx store.Tx, id Identity) error {
	p, err := r.Get(tx, id)
	if err != nil {
		return err
	}
	if p.Builtin {
		return ErrReadOnly
	}
	t, scope, err := transaction(tx)
	if err != nil {
		return err
	}
	var count int
	if err = t.QueryRow("SELECT COUNT(*) FROM registry_reference WHERE kind IN ('plugin','ruleset') AND resource=? AND version=? AND digest=?", id.ID, id.Version, id.Digest).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrReferenced
	}
	for _, statement := range []string{
		"DELETE FROM registry_default WHERE id=? AND version=? AND digest=?",
		"DELETE FROM registry_grant WHERE id=? AND version=? AND digest=?",
		"DELETE FROM registry_plugin WHERE id=? AND version=? AND digest=?",
	} {
		if _, err = scope.Exec(statement, id.ID, id.Version, id.Digest); err != nil {
			return err
		}
	}
	changed(tx, p)
	return nil
}

// References lists what still pins a plugin or rule set version, across both
// workspaces, so an uninstall that is refused can say what holds it (FR-42).
func (r *Repository) References(tx store.Tx, id Identity) ([]Reference, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT workspace,owner,kind,resource,version,digest FROM registry_reference
		WHERE kind IN ('plugin','ruleset') AND resource=? AND version=? AND digest=? ORDER BY workspace, owner`, id.ID, id.Version, id.Digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reference
	for rows.Next() {
		var x Reference
		if err = rows.Scan(&x.Workspace, &x.Owner, &x.Kind, &x.Resource, &x.Version, &x.Digest); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Versions lists the installed versions of one plugin or rule set id, newest
// registration last, for upgrade and rollback.
func (r *Repository) Versions(tx store.Tx, id string) ([]Plugin, error) {
	t, _, err := transaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query("SELECT version, digest FROM registry_plugin WHERE id=? ORDER BY rowid", id)
	if err != nil {
		return nil, err
	}
	var ids []Identity
	for rows.Next() {
		x := Identity{ID: id}
		if err = rows.Scan(&x.Version, &x.Digest); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, x)
	}
	rows.Close()
	var out []Plugin
	for _, x := range ids {
		p, err := r.Get(tx, x)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

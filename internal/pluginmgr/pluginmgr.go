package pluginmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/registry"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrConfig      = errors.New("PLUGIN_MANAGER_CONFIG_INVALID")
	ErrNoCandidate = errors.New("PLUGIN_CANDIDATE_UNKNOWN")
	ErrRulePack    = errors.New("RULEPACK_INVALID")
	ErrDeploy      = errors.New("PLUGIN_DEPLOY_FAILED")
	ErrNotReady    = errors.New("PLUGIN_READINESS_FAILED")
	ErrBuiltin     = errors.New("PLUGIN_BUILTIN_PROTECTED")
	ErrRollback    = errors.New("PLUGIN_ROLLBACK_UNAVAILABLE")
)

// Source says where a package comes from. Exactly one of Path and GitURL is set. A
// Git source names a full commit (a branch or tag moves; a commit does not), and an
// expected digest, if the user gives one, must match what was fetched.
type Source struct {
	Path   string
	GitURL string
	Commit string
	Digest string
}

// Inspection is what the user confirms before anything is deployed (FR-41).
type Inspection struct {
	Token        string
	ID, Version  string
	Kind         string
	License      string
	Roles        []string
	Formats      []string
	Platforms    []string
	Digest       string
	NeedsNetwork bool
	Packages     int
	Origin       string // "path" or "git:<commit>"
}

type candidate struct {
	insp     Inspection
	dir      string
	manifest []byte
}

// Config wires the manager to the modules it may use. Deploy and Probe are the
// parts that need processes: environment deployment (envs through the installer
// shipped with the application) and the readiness check (host starting a sandboxed
// instance, handshake, capability check and a fixed sample).
type Config struct {
	Dir      string // absolute; holds staging/ and plugins/
	Gate     *netgate.Gate
	Registry *registry.Repository
	Commit   func(ctx context.Context, fn func(store.Tx) error) error
	// Git fetches exactly one commit into dest. It runs under a network lease.
	Git func(ctx context.Context, url, commit, dest string) error
	// Deploy builds the plugin's environment from its lock, wheels only, and returns
	// the environment digest. It runs under a maintenance lease (and a network lease
	// when the lock needs downloads).
	Deploy func(ctx context.Context, snapshot string, m registry.Manifest) (env string, err error)
	// Cleanup removes a deployed environment that registration did not adopt, or
	// that an uninstall left unreferenced.
	Cleanup func(ctx context.Context, env string) error
	// Probe is the readiness check of a deployed plugin.
	Probe func(ctx context.Context, snapshot string, m registry.Manifest, env string) error
}

type Manager struct {
	cfg   Config
	mu    sync.Mutex
	cands map[string]*candidate
}

func New(cfg Config) (*Manager, error) {
	if !filepath.IsAbs(cfg.Dir) || cfg.Gate == nil || cfg.Registry == nil || cfg.Commit == nil || cfg.Deploy == nil || cfg.Cleanup == nil || cfg.Probe == nil {
		return nil, ErrConfig
	}
	for _, d := range []string{"staging", "plugins"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, d), 0o700); err != nil {
			return nil, ErrConfig
		}
	}
	return &Manager{cfg: cfg, cands: map[string]*candidate{}}, nil
}

func token() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) staged(token string) string { return filepath.Join(m.cfg.Dir, "staging", token) }

// Prepare fetches and inspects a package. Nothing is installed: the result goes to
// the user, and Confirm continues. A failed inspection leaves no staging behind.
func (m *Manager) Prepare(ctx context.Context, src Source) (Inspection, error) {
	switch {
	case (src.Path == "") == (src.GitURL == ""), src.Path != "" && !filepath.IsAbs(src.Path), src.Digest != "" && !strings.HasPrefix(src.Digest, "sha256:"):
		return Inspection{}, ErrSource
	case src.GitURL != "" && !commitRe.MatchString(src.Commit):
		return Inspection{}, ErrCommit
	}
	tok := token()
	dir := m.staged(tok)
	fail := func(err error) (Inspection, error) { os.RemoveAll(dir); return Inspection{}, err }
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(ErrSource)
	}
	origin := "path"
	if src.Path != "" {
		if err := copyTree(src.Path, dir); err != nil {
			return fail(err)
		}
	} else {
		origin = "git:" + src.Commit
		lease, err := m.cfg.Gate.Acquire(netgate.Network)
		if err != nil {
			return fail(err)
		}
		err = m.cfg.Git(ctx, src.GitURL, src.Commit, dir)
		lease.Release()
		if err != nil {
			return fail(ErrSource)
		}
		os.RemoveAll(filepath.Join(dir, ".git"))
	}
	digest, err := treeDigest(dir)
	if err != nil {
		return fail(err)
	}
	if src.Digest != "" && src.Digest != digest {
		return fail(ErrDigest)
	}
	insp, data, err := m.inspect(dir, digest, origin)
	if err != nil {
		return fail(err)
	}
	insp.Token = tok
	m.mu.Lock()
	m.cands[tok] = &candidate{insp: insp, dir: dir, manifest: data}
	m.mu.Unlock()
	return insp, nil
}

func (m *Manager) inspect(dir, digest, origin string) (Inspection, []byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || len(data) > 1<<20 {
		return Inspection{}, nil, ErrLayout
	}
	if err = checkLayout(dir); err != nil {
		return Inspection{}, nil, err
	}
	var man registry.Manifest
	if json.Unmarshal(data, &man) != nil {
		return Inspection{}, nil, registry.ErrInvalid
	}
	insp := Inspection{ID: man.ID, Version: man.Version, Kind: man.Kind, License: man.License, Roles: man.Roles, Formats: man.Formats, Platforms: man.Platforms, Digest: digest, Origin: origin}
	switch man.Kind {
	case "ruleset":
		if man.Runtime != nil {
			return Inspection{}, nil, registry.ErrInvalid // rule data only: no runtime, no environment
		}
		if err := checkRulePack(dir); err != nil {
			return Inspection{}, nil, err
		}
	case "plugin":
		lock, err := os.ReadFile(filepath.Join(dir, "uv.lock"))
		if err != nil {
			return Inspection{}, nil, ErrLock
		}
		l, err := checkLock(string(lock))
		if err != nil {
			return Inspection{}, nil, err
		}
		insp.NeedsNetwork, insp.Packages = l.NeedsNetwork, l.Packages
	}
	// the manifest is validated by the registry's own schema and platform rules here, so a
	// package that could never be registered is refused before it is deployed
	if err := m.cfg.Registry.Check(data); err != nil {
		return Inspection{}, nil, err
	}
	return insp, data, nil
}

// Cancel discards a prepared candidate.
func (m *Manager) Cancel(tok string) {
	m.mu.Lock()
	c := m.cands[tok]
	delete(m.cands, tok)
	m.mu.Unlock()
	if c != nil {
		os.RemoveAll(c.dir)
	}
}

func checkRulePack(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "rules.json"))
	if err != nil || len(data) > 8<<20 {
		return ErrRulePack
	}
	_, set, err := rules.ParsePack(data)
	if err != nil || len(set) == 0 {
		return ErrRulePack
	}
	if _, err := rules.Compile(set, rules.DefaultBudget()); err != nil {
		return ErrRulePack
	}
	return nil
}

// Confirm installs a prepared candidate: deploy, check readiness, register. Any
// failure rolls the half-product back and leaves other plugins and the built-in
// environments untouched. The registered version starts disabled and is not made a
// default.
func (m *Manager) Confirm(ctx context.Context, tok string) (registry.Plugin, error) {
	m.mu.Lock()
	c := m.cands[tok]
	delete(m.cands, tok)
	m.mu.Unlock()
	if c == nil {
		return registry.Plugin{}, ErrNoCandidate
	}
	final := filepath.Join(m.cfg.Dir, "plugins", strings.ReplaceAll(c.insp.ID, "/", "_")+"_"+c.insp.Version+"_"+c.insp.Digest[len("sha256:"):][:16])
	if _, err := os.Stat(final); err == nil {
		os.RemoveAll(c.dir)
		return registry.Plugin{}, registry.ErrConflict
	}
	var man registry.Manifest
	json.Unmarshal(c.manifest, &man)
	maint, err := m.cfg.Gate.Acquire(netgate.Maintenance)
	if err != nil {
		os.RemoveAll(c.dir)
		return registry.Plugin{}, err
	}
	defer maint.Release()
	var net *netgate.Lease
	if c.insp.NeedsNetwork {
		if net, err = m.cfg.Gate.Acquire(netgate.Network); err != nil {
			os.RemoveAll(c.dir)
			return registry.Plugin{}, err
		}
		defer net.Release()
	}
	if err := os.Rename(c.dir, final); err != nil {
		os.RemoveAll(c.dir)
		return registry.Plugin{}, ErrDeploy
	}
	rollback := func(env string) {
		os.RemoveAll(final)
		if env != "" {
			m.cfg.Cleanup(context.WithoutCancel(ctx), env)
		}
	}
	env := ""
	if c.insp.Kind == "plugin" {
		if env, err = m.cfg.Deploy(ctx, final, man); err != nil {
			rollback("")
			return registry.Plugin{}, ErrDeploy
		}
		if err = m.cfg.Probe(ctx, final, man, env); err != nil {
			rollback(env)
			return registry.Plugin{}, ErrNotReady
		}
	}
	var p registry.Plugin
	if err = m.cfg.Commit(ctx, func(tx store.Tx) (err error) {
		p, err = m.cfg.Registry.Register(tx, c.manifest, c.insp.Digest, env, false)
		return err
	}); err != nil {
		rollback(env)
		if errors.Is(err, registry.ErrConflict) || errors.Is(err, registry.ErrSandbox) || errors.Is(err, registry.ErrInvalid) || errors.Is(err, registry.ErrPlatform) {
			return registry.Plugin{}, err
		}
		return registry.Plugin{}, ErrDeploy
	}
	return p, nil
}

func (m *Manager) run(ctx context.Context, fn func(tx store.Tx) error) error {
	return m.cfg.Commit(ctx, fn)
}

// Enable makes a version usable. Whether it becomes a default stays a separate,
// explicit choice (FR-43).
func (m *Manager) Enable(ctx context.Context, id registry.Identity) error {
	return m.run(ctx, func(tx store.Tx) error { return m.cfg.Registry.Enable(tx, id, true) })
}

// Disable stops a version being offered and returns what still holds it: tasks that
// froze it keep working with it, so disabling never changes them (INV-03).
func (m *Manager) Disable(ctx context.Context, id registry.Identity) ([]registry.Reference, error) {
	var refs []registry.Reference
	err := m.run(ctx, func(tx store.Tx) error {
		p, err := m.cfg.Registry.Get(tx, id)
		if err != nil {
			return err
		}
		if p.Builtin {
			return ErrBuiltin
		}
		if err = m.cfg.Registry.Enable(tx, id, false); err != nil {
			return err
		}
		refs, err = m.cfg.Registry.References(tx, id)
		return err
	})
	return refs, err
}

// ReferencedError names what blocks an uninstall so the user can wait or void.
type ReferencedError struct{ Refs []registry.Reference }

func (e *ReferencedError) Error() string { return registry.ErrReferenced.Error() }
func (e *ReferencedError) Unwrap() error { return registry.ErrReferenced }

// Uninstall removes a version unless something still depends on it. The files and the
// environment go only after the registry row is gone.
func (m *Manager) Uninstall(ctx context.Context, id registry.Identity) error {
	var p registry.Plugin
	var refs []registry.Reference
	err := m.run(ctx, func(tx store.Tx) (err error) {
		if p, err = m.cfg.Registry.Get(tx, id); err != nil {
			return err
		}
		if p.Builtin {
			return ErrBuiltin
		}
		if refs, err = m.cfg.Registry.References(tx, id); err != nil {
			return err
		}
		if len(refs) > 0 {
			return &ReferencedError{Refs: refs}
		}
		return m.cfg.Registry.Remove(tx, id)
	})
	if err != nil {
		return err
	}
	os.RemoveAll(filepath.Join(m.cfg.Dir, "plugins", strings.ReplaceAll(id.ID, "/", "_")+"_"+id.Version+"_"+id.Digest[len("sha256:"):][:16]))
	if p.Environment != "" {
		m.cfg.Cleanup(context.WithoutCancel(ctx), p.Environment) // best effort: envs reclaims what is left
	}
	return nil
}

// Rollback re-enables an older installed version and disables the newer one. The
// older one must still be installed: an upgrade keeps it until the user removes it.
// Completion grants are bound to a version and digest, so neither direction carries
// one over (FR-44).
func (m *Manager) Rollback(ctx context.Context, from, to registry.Identity) error {
	if from.ID != to.ID || from == to {
		return ErrRollback
	}
	return m.run(ctx, func(tx store.Tx) error {
		if _, err := m.cfg.Registry.Get(tx, to); err != nil {
			return ErrRollback
		}
		if err := m.cfg.Registry.Enable(tx, to, true); err != nil {
			return err
		}
		return m.cfg.Registry.Enable(tx, from, false)
	})
}

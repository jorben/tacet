package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jorben/tacet/internal/lifecycle"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/policy"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/registry"
	"github.com/jorben/tacet/internal/retention"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/rules"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/validate"
	"github.com/jorben/tacet/internal/workspace"
)

var (
	ErrOptions = errors.New("APP_OPTIONS_INVALID")
	ErrClosed  = errors.New("APP_CLOSED")
)

// Builtin is one built-in format plugin of the release group.
type Builtin struct {
	Module   string   // python module started with -I -m
	Manifest []byte   // manifest.json
	Formats  []string // file extensions it takes (without dot)
}

// ModelOption names the fixed model resource and the detection plugin that runs it.
type ModelOption struct {
	Dir          string  // absolute directory with the model files
	ManifestPath string  // absolute path of the integrity manifest (resources/models/*.json)
	Plugin       Builtin // the detection plugin (role detect); Formats is empty
}

// modelInfo is what the engine knows about its model after reading the manifest.
type modelInfo struct {
	id, digest, backend string
	floor, recommended  float64
	categories          []string
	labels              map[string]string
	plug                plugin
	sem                 chan struct{} // one model worker at a time: its memory is not shared
}

// Options configure one engine over one workspace directory.
type Options struct {
	Dir       string
	UserKey   keys.UserKeyStore
	Python    string // absolute interpreter of the built-in environment
	EnvDigest string // "sha256:..." identity of that environment
	Plugins   []Builtin
	RulePack  []byte // built-in rule pack (resources/rules/cn-office-v1.json)
	Create    bool
	Platform  string // darwin-arm64 or windows-amd64
	// Model, when set, adds the model-assisted preset. The model files are verified by the
	// detection plugin against ManifestPath before anything is loaded; a model that fails
	// verification fails the stage, it never counts as "nothing found".
	Model  *ModelOption
	Budget sched.Budget
	// Now is the clock of retention (tests move it); nil is the real clock.
	Now func() time.Time
	// Trace, when set, is told how the start-up order and the exit order progress.
	Trace func(lifecycle.Status)
}

// Engine is the shared kernel of the desktop app and the CLI: the same
// stages, the same verdicts (AR-10).
type Engine struct {
	opt    Options
	ws     *workspace.Workspace
	jobs   *jobs.Repository
	queue  *sched.Queue
	disp   *sched.Dispatcher
	gate   *netgate.Gate
	refs   *refs.Manager
	cfg    *config.Repository
	reg    *registry.Repository
	snap   snapshot.Repository
	revs   *review.Service
	exc    exceptions.Repository
	fr     *findings.Repository
	cov    coverage.Repository
	units  extract.Repository
	pol    *policy.Service
	pub    *publish.Service
	exp    *export.Service
	val    *validate.Service
	pipe   *extract.Pipeline
	runner *validate.Runner

	mu        sync.Mutex
	secrets   map[string][]byte // job -> password waiting for its preflight; memory only
	plugins   map[string]plugin // extension -> plugin
	detect    map[string]*detectCache
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
	listeners map[int]func(batch string)
	listenSeq int
	pv        *previewState
	model     *modelInfo
	ret       *retention.Service
	budget    sched.Budget
	// beforeConfirm, when set by a test, runs before a file that finished its inspection after
	// the batch started joins the batch; it holds that window open.
	beforeConfirm func()
}

type plugin struct {
	module string
	id     snapshot.PluginRef
	man    registry.Manifest
}

func allModules() []store.Module {
	return []store.Module{jobs.Module(), snapshot.Module(), config.Module(), registry.Module(), refs.Module(), findings.Module(), coverage.Module(),
		extract.Module(), review.Module(), exceptions.Module(), policy.Module(), export.Module(), validate.Module(), publish.Module(), retention.Module(), appModule()}
}

// Open takes the workspace, restores keys and files, seeds the built-in
// configuration and starts the scheduler loop. Close releases everything.
func Open(ctx context.Context, opt Options) (*Engine, error) {
	if opt.Dir == "" || !filepath.IsAbs(opt.Dir) || opt.UserKey == nil || !filepath.IsAbs(opt.Python) || opt.EnvDigest == "" || len(opt.Plugins) == 0 || len(opt.RulePack) == 0 ||
		(opt.Platform != "darwin-arm64" && opt.Platform != "windows-amd64") {
		return nil, ErrOptions
	}
	if opt.Model != nil && (!filepath.IsAbs(opt.Model.Dir) || !filepath.IsAbs(opt.Model.ManifestPath)) {
		return nil, ErrOptions
	}
	if opt.Create {
		if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
			return nil, err
		}
	}
	e := &Engine{opt: opt, gate: netgate.NewGate(), plugins: map[string]plugin{}, detect: map[string]*detectCache{}, secrets: map[string][]byte{}, listeners: map[int]func(string){}, done: make(chan struct{})}
	reg, err := store.NewRegistry(allModules()...)
	if err != nil {
		return nil, err
	}
	if e.pv, err = newPreviewState(); err != nil {
		return nil, err
	}
	e.jobs = jobs.NewRepository(e.dispatch, jobs.DefaultLimits())
	// The start-up order of module design 4.3. A step runs only when the one before it succeeded, and the
	// engine is handed out only when all six did.
	starter, err := lifecycle.NewStarter([]lifecycle.Step{
		{Name: lifecycle.StepWorkspace, Run: func(ctx context.Context) error { return e.openWorkspace(ctx, reg) }},
		{Name: lifecycle.StepReap, Run: e.reapPrevious},
		{Name: lifecycle.StepPublish, Run: e.settlePublish},
		{Name: lifecycle.StepSweep, Run: e.applyRetention},
		{Name: lifecycle.StepRecover, Run: e.recoverAll},
		{Name: lifecycle.StepDispatch, Run: e.startDispatch},
	}, e.opt.Trace)
	if err != nil {
		return nil, err
	}
	if err = starter.Start(ctx); err != nil {
		if e.ws != nil {
			e.ws.Close()
		}
		var se *lifecycle.StepError
		if errors.As(err, &se) {
			return nil, se.Err // callers act on the cause (workspace busy, key missing, …)
		}
		return nil, err
	}
	return e, nil
}

// openWorkspace is the first step: take the workspace, restore its keys and files, register what is built
// in, and build the services that need the workspace.
func (e *Engine) openWorkspace(ctx context.Context, reg *store.Registry) error {
	opt := e.opt
	var err error
	e.ws, err = workspace.OpenManaged(ctx, opt.Dir, workspace.Managed{UserKey: opt.UserKey, Registry: reg, Versions: versions{e}, Create: opt.Create})
	if err != nil {
		e.ws = nil
		return err
	}
	resDir := filepath.Join(opt.Dir, "resources")
	if err = os.MkdirAll(resDir, 0o700); err != nil {
		return err
	}
	if e.refs, err = refs.New(resDir, e.ws.Admit, e.taskKey); err != nil {
		return err
	}
	e.refs.UseCatalog(refs.NewSQLCatalog(e.ws.Commit))
	if _, err = e.refs.Recover(ctx); err != nil {
		return err
	}
	validator, err := config.NewValidator(rules.DefaultBudget())
	if err != nil {
		return err
	}
	e.cfg = config.NewRepository(validator.Validate)
	if e.reg, err = registry.NewRepository(opt.Platform, func() bool { return false }); err != nil {
		return err
	}
	if err = e.seed(ctx); err != nil {
		return err
	}
	if e.fr, err = findings.NewRepository(e.batchKey); err != nil {
		return err
	}
	return e.build()
}

func (e *Engine) taskKey(job string) ([]byte, error)    { return e.ws.Keys().Get(keys.Task, job) }
func (e *Engine) batchKey(batch string) ([]byte, error) { return e.ws.Keys().Get(keys.Batch, batch) }

// seed registers the built-in plugins and the built-in configuration objects.
func (e *Engine) seed(ctx context.Context) error {
	return e.ws.Commit(ctx, func(tx store.Tx) error {
		for _, b := range e.opt.Plugins {
			var m registry.Manifest
			if json.Unmarshal(b.Manifest, &m) != nil {
				return ErrOptions
			}
			digest := "sha256:" + hexSum(b.Manifest, []byte(e.opt.EnvDigest))
			p, err := e.reg.Register(tx, b.Manifest, digest, e.opt.EnvDigest, true)
			if errors.Is(err, registry.ErrConflict) {
				if p, err = e.reg.Get(tx, registry.Identity{ID: m.ID, Version: m.Version, Digest: digest}); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			if err = e.reg.Enable(tx, p.Identity, true); err != nil {
				return err
			}
			ref := snapshot.PluginRef{ID: p.ID, Version: p.Version, Digest: p.Digest, Env: p.Environment}
			for _, f := range b.Formats {
				e.plugins[f] = plugin{module: b.Module, id: ref, man: p.Manifest}
			}
		}
		if e.opt.Model != nil {
			if err := e.registerModel(tx); err != nil {
				return err
			}
		}
		return seedConfig(tx, e.cfg, e.opt.RulePack, e.model)
	})
}

// labelCategories is how the fixed model's labels become Tacet categories.
var labelCategories = map[string]string{
	"private_person": "person", "private_address": "address", "private_date": "date", "phone": "phone", "email": "email",
	"account": "account", "payment_card": "payment_card", "ip_address": "ip_address", "url": "url", "common_secret": "secret",
}

// registerModel reads the integrity manifest and registers the detection plugin.
func (e *Engine) registerModel(tx store.Tx) error {
	o := e.opt.Model
	raw, err := os.ReadFile(o.ManifestPath)
	if err != nil {
		return ErrOptions
	}
	var man struct {
		ID       string  `json:"id"`
		Backend  string  `json:"backend"`
		Variant  string  `json:"variant"`
		Version  string  `json:"version"`
		Upstream float64 `json:"upstream_recommended_score"`
	}
	if json.Unmarshal(raw, &man) != nil || man.ID == "" || man.Upstream <= 0 || man.Upstream > 1 {
		return ErrOptions
	}
	var pm registry.Manifest
	if json.Unmarshal(o.Plugin.Manifest, &pm) != nil {
		return ErrOptions
	}
	digest := "sha256:" + hexSum(o.Plugin.Manifest, []byte(e.opt.EnvDigest))
	p, err := e.reg.Register(tx, o.Plugin.Manifest, digest, e.opt.EnvDigest, true)
	if errors.Is(err, registry.ErrConflict) {
		p, err = e.reg.Get(tx, registry.Identity{ID: pm.ID, Version: pm.Version, Digest: digest})
	}
	if err != nil {
		return err
	}
	if err = e.reg.Enable(tx, p.Identity, true); err != nil {
		return err
	}
	m := &modelInfo{id: man.ID + "-" + man.Variant, digest: "sha256:" + hexSum(raw), backend: man.Backend, floor: 0.5, recommended: man.Upstream,
		labels: labelCategories, sem: make(chan struct{}, 1),
		plug: plugin{module: o.Plugin.Module, id: snapshot.PluginRef{ID: p.ID, Version: p.Version, Digest: p.Digest, Env: p.Environment}, man: p.Manifest}}
	seen := map[string]bool{}
	for _, c := range labelCategories {
		if !seen[c] {
			seen[c] = true
			m.categories = append(m.categories, c)
		}
	}
	sort.Strings(m.categories)
	e.model = m
	return nil
}

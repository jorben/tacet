package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/lifecycle"
	"github.com/jorben/tacet/internal/platform"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jorben/tacet/internal/categories"
	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/models"
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

// ModelOption names one model of the catalog: its integrity manifest (resources/models/*.json, shipped
// with the application) and the detection plugin that runs it. The files are downloaded by the user into
// the models folder; Dir, when set, names files that are already present instead (development, tests).
type ModelOption struct {
	Dir          string  // absolute directory of preinstalled files; "" = downloaded
	ManifestPath string  // absolute path of the integrity manifest
	Plugin       Builtin // the detection plugin (role detect); Formats is empty
	// Accel is retained for resolving historical snapshots. The production catalog uses one
	// GPU-compatible build per model and changes only the execution provider.
	Accel *ModelVariant
}

// ModelVariant is another build of a model: its files and integrity manifest.
type ModelVariant struct {
	Dir          string // absolute directory of preinstalled files; "" = downloaded
	ManifestPath string // absolute path of its integrity manifest
}

// ModelSource is where downloads come from. Production uses Hugging Face at the manifest's pinned
// revision; tests point it at a local server.
type ModelSource struct {
	Base  string   // "https://huggingface.co"
	Hosts []string // network allowlist, redirect hosts included
	TLS   *tls.Config
}

// modelInfo is what the engine knows about one model of the catalog after reading its manifest.
type modelInfo struct {
	id, digest, backend string
	name, version       string
	variant, languages  string
	repository          string // "owner/name" on Hugging Face, from the manifest; empty when not well formed
	floor, recommended  float64
	categories          []string              // what its labels produce, sorted
	labels              map[string]string     // label -> category, from the manifest
	declared            []categories.Category // categories the manifest adds to the standard vocabulary
	plug                plugin
	opt                 *ModelOption
	kind                modelKind
	entry               models.Entry // its catalog entry: the download key and the digest of its files
	minMemory           uint64       // installed memory a machine needs to download it; 0 = any
	accel               *variantInfo // the accelerated build, when this installation offers one
}

// variantInfo is what the engine knows about an accelerated build after reading its manifest.
type variantInfo struct {
	id, digest, backend string
	dir, manifest       string // dir "" = downloaded
	entry               models.Entry
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
	// Models is the model catalog; each adds its model-assisted preset. The model files are verified by
	// the detection plugin against the manifest before anything is loaded; a model that fails
	// verification fails the stage, it never counts as "nothing found".
	Models []*ModelOption
	// ModelsRoot is the folder downloaded models are kept in, shared by the workspaces of one user;
	// "" is a folder inside the workspace.
	ModelsRoot string
	// ModelSource is where models are downloaded from; nil is Hugging Face.
	ModelSource *ModelSource
	// Memory reports the machine's installed memory; nil is platform.PhysicalMemory.
	Memory func() (uint64, error)
	Budget sched.Budget
	// Now is the clock of retention (tests move it); nil is the real clock.
	Now func() time.Time
	// Trace, when set, is told how the start-up order and the exit order progress.
	Trace func(lifecycle.Status)
	// Activity declares to the platform that work runs (on) or no longer does (off); nil is
	// platform.DeclareActivity.
	Activity func(on bool) error
	// ProbeDevices: start-up device probe; only the desktop app shows the device, the CLI and tests leave it off.
	ProbeDevices bool
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
	act    *activity
	exp    *export.Service
	val    *validate.Service
	pipe   *extract.Pipeline
	runner *validate.Runner

	mu            sync.Mutex
	secrets       map[string][]byte // job -> password waiting for its preflight; memory only
	plugins       map[string]plugin // extension -> plugin
	detect        map[string]*detectCache
	modelBackends map[string]string // actual providers, keyed by model digest and requested policy
	probing       map[string]bool   // models whose device the start-up probe has yet to confirm
	probeFailed   map[string]string // model id -> reason code of the probe's last failure to confirm it
	probeStop     context.CancelFunc
	probeHolds    bool          // the probe holds modelSem right now (observed by tests); probeStop cancels the whole probe
	probeDone     chan struct{} // closed when the start-up probe has ended; nil if none ran (set before Open returns)
	probeLaunch   sync.Mutex    // held by the probe while it checks it was not stopped and starts its worker
	cancel        context.CancelFunc
	done          chan struct{}
	closed        bool
	listeners     map[int]func(batch string)
	listenSeq     int
	pv            *previewState
	models        []*modelInfo    // the catalog, in manifest order
	modelSem      chan struct{}   // one model worker at a time (detection, preparation, the start-up probe, which yields): their memory is not shared
	catalog       *models.Service // downloads and installed state; nil when every model is preinstalled
	life          context.Context // the engine's lifetime: background downloads run under it
	ret           *retention.Service
	budget        sched.Budget
	// beforeConfirm, when set by a test, runs before a file that finished its inspection after
	// the batch started joins the batch; it holds that window open.
	beforeConfirm func()
	// beforeDispatch, when set by a test, runs before a stage is begun. A slow one stands in for a loaded
	// machine, where a file waits in the queue for a while before the scheduler takes it.
	beforeDispatch func(kernel.StageKind)
	// afterReap, in tests, runs between reclaiming the workers and the next exit step.
	afterReap func()
	// beforeProbeLaunch, in tests, runs when the start-up probe has the model slot and is about to start its worker,
	// before the section that starts it (probeLaunch). inProbeLaunch runs inside that section, after its stop check.
	beforeProbeLaunch func(ctx context.Context)
	inProbeLaunch     func(ctx context.Context)
	// deadline, in tests, replaces stageDeadline.
	deadline func(kernel.StageKind, int64) time.Duration
}

type plugin struct {
	module string
	id     snapshot.PluginRef
	man    registry.Manifest
}

func allModules() []store.Module {
	return []store.Module{jobs.Module(), snapshot.Module(), config.Module(), registry.Module(), refs.Module(), findings.Module(), coverage.Module(),
		extract.Module(), review.Module(), exceptions.Module(), policy.Module(), export.Module(), validate.Module(), publish.Module(), retention.Module(), models.Module(), appModule()}
}

// Open takes the workspace, restores keys and files, seeds the built-in
// configuration and starts the scheduler loop. Close releases everything.
func Open(ctx context.Context, opt Options) (*Engine, error) {
	if opt.Dir == "" || !filepath.IsAbs(opt.Dir) || opt.UserKey == nil || !filepath.IsAbs(opt.Python) || opt.EnvDigest == "" || len(opt.Plugins) == 0 || len(opt.RulePack) == 0 ||
		(opt.Platform != "darwin-arm64" && opt.Platform != "windows-amd64") {
		return nil, ErrOptions
	}
	for _, m := range opt.Models {
		if m == nil || (m.Dir != "" && !filepath.IsAbs(m.Dir)) || !filepath.IsAbs(m.ManifestPath) ||
			(m.Accel != nil && ((m.Accel.Dir != "" && !filepath.IsAbs(m.Accel.Dir)) || !filepath.IsAbs(m.Accel.ManifestPath))) {
			return nil, ErrOptions
		}
	}
	if opt.ModelsRoot != "" && !filepath.IsAbs(opt.ModelsRoot) {
		return nil, ErrOptions
	}
	if opt.Create {
		if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
			return nil, err
		}
	}
	e := &Engine{opt: opt, gate: netgate.NewGate(), plugins: map[string]plugin{}, detect: map[string]*detectCache{}, secrets: map[string][]byte{}, listeners: map[int]func(string){}, done: make(chan struct{}),
		modelSem: make(chan struct{}, 1), life: context.Background()}
	declare := opt.Activity
	if declare == nil {
		declare = platform.DeclareActivity
	}
	e.act = newActivity(declare, activityLinger)
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
	if err = e.openCatalog(ctx); err != nil {
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
		if err := e.registerModels(tx); err != nil {
			return err
		}
		return seedConfig(tx, e.cfg, e.opt.RulePack, e.models)
	})
}

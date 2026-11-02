package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jorben/tacet/internal/categories"
	"github.com/jorben/tacet/internal/detect"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/models"
	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/registry"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
)

// The model catalog: every model the application knows ships as a small integrity manifest; its files are
// downloaded by the user from Hugging Face at the manifest's pinned revision, checked file by file against
// the manifest, and installed atomically (internal/models). Nothing is downloaded without an explicit action.

var (
	ErrModelNotInstalled = errors.New("MODEL_NOT_INSTALLED")       // a preset's model is not downloaded
	ErrModelMemory       = errors.New("MODEL_MEMORY_INSUFFICIENT") // the machine has less memory than the model needs
	ErrModelUnknown      = errors.New("MODEL_UNKNOWN")
)

// modelKind is what the application adds to a model's manifest: its built-in configuration objects, the
// memory a run reserves for it and its trial sample. How its labels become categories is the manifest's
// label_map, so a model with the same adapter needs no change here to report other categories.
type modelKind struct {
	profileID, schemeID, presetID   string
	profileName, schemeName, preset string
	memory                          int64 // what a run reserves for the model
	presetMemory                    int64 // the preset's budget for the whole run
	trialText                       string
	trialWant                       map[string]string // label -> value the trial must find
}

// Built-in object ids of the AstrLink preset (kept from before there was a catalog).
const (
	ModelProfileID = "builtin-model-astrlink"
	ModelSchemeID  = "builtin-scheme-model"
	ModelPresetID  = "builtin-preset-model"
)

var modelKinds = map[string]modelKind{
	"astrlink-guard": {
		profileID: ModelProfileID, schemeID: ModelSchemeID, presetID: ModelPresetID,
		profileName: "AstrLink Guard（固定版本）", schemeName: "占位符方案（含模型类别）", preset: "办公外发（规则 + 模型）",
		// the measured peak of the model (about 175 MB resident, docs/v1/backend/m0-progress.md) with headroom
		memory: 400 << 20, presetMemory: 1 << 30,
		trialText: "请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。",
		trialWant: map[string]string{"private_person": "张伟", "phone": "13812345678", "email": "zhangwei@example.com"},
	},
	"privacy-filter": {
		profileID: "builtin-model-privacy-filter", schemeID: "builtin-scheme-privacy-filter", presetID: "builtin-preset-privacy-filter",
		profileName: "OpenAI Privacy Filter（固定版本）", schemeName: "占位符方案（Privacy Filter 类别）", preset: "办公外发（规则 + Privacy Filter）",
		// measured peak 2.2-2.3 GiB with 512-token windows, flat up to 100k tokens (docs/v1/backend/m0-decisions.md)
		memory: 3 << 30, presetMemory: 4 << 30,
		trialText: "Contact Jane Doe at jane.doe@example.org or +1 415-555-0132.",
		trialWant: map[string]string{"private_person": "Jane Doe", "private_email": "jane.doe@example.org", "private_phone": "+1 415-555-0132"},
	},
}

// modelManifest is what the engine reads from a manifest beyond what internal/models checks.
type modelManifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Variant     string            `json:"variant"`
	Languages   string            `json:"languages"`
	Backend     string            `json:"backend"`
	Repository  string            `json:"repository"`
	Revision    string            `json:"revision"`
	Upstream    *float64          `json:"upstream_recommended_score"`
	DefaultMin  *float64          `json:"default_min_score"`
	Floor       *float64          `json:"adapter_floor"`
	MinMemory   uint64            `json:"min_memory_bytes"`
	Accelerates string            `json:"accelerates"`
	SourceDir   *string           `json:"source_dir"`   // folder of the files in the repository; default the variant
	SourcePaths map[string]string `json:"source_paths"` // per-file path in the repository, where it differs
	// LabelMap maps each model label to a category id: a standard one or one Categories declares.
	LabelMap map[string]string `json:"label_map"`
	// Categories declares categories the standard vocabulary does not have.
	Categories []categories.Decl `json:"categories"`
	Files      map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func readManifest(path string) ([]byte, modelManifest, models.Entry, error) {
	var m modelManifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, m, models.Entry{}, ErrOptions
	}
	entry, err := models.ParseManifest(raw)
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return nil, m, models.Entry{}, ErrOptions
	}
	return raw, m, entry, nil
}

// modelCategories checks a manifest's label map: every label maps to a standard category or to one the
// manifest declares, and the trial sample's labels are mapped. It returns the declared categories and the
// sorted categories the labels produce.
func modelCategories(man modelManifest, kind modelKind) ([]categories.Category, []string, error) {
	declared, err := categories.FromDecls(man.Categories, categories.Model)
	if err != nil || len(man.LabelMap) == 0 {
		return nil, nil, ErrOptions
	}
	known := categories.Merge(categories.StandardVocabulary(), declared)
	seen := map[string]bool{}
	var cats []string
	for label, c := range man.LabelMap {
		if label == "" || !known.Has(c) {
			return nil, nil, ErrOptions
		}
		if !seen[c] {
			seen[c] = true
			cats = append(cats, c)
		}
	}
	for label := range kind.trialWant {
		if _, ok := man.LabelMap[label]; !ok {
			return nil, nil, ErrOptions
		}
	}
	sort.Strings(cats)
	return declared, cats, nil
}

// registerModels reads every manifest of the catalog and registers the detection plugins.
func (e *Engine) registerModels(tx store.Tx) error {
	e.models = nil
	for _, o := range e.opt.Models {
		raw, man, entry, err := readManifest(o.ManifestPath)
		if err != nil {
			return err
		}
		kind, ok := modelKinds[man.ID]
		if !ok {
			return ErrOptions
		}
		recommended := 0.0
		switch {
		case man.Upstream != nil:
			recommended = *man.Upstream
		case man.DefaultMin != nil:
			recommended = *man.DefaultMin
		}
		floor := 0.5
		if man.Floor != nil {
			floor = *man.Floor
		}
		if recommended <= 0 || recommended > 1 || floor <= 0 || floor > recommended {
			return ErrOptions
		}
		declared, cats, err := modelCategories(man, kind)
		if err != nil {
			return err
		}
		p, err := e.registerPlugin(tx, o.Plugin)
		if err != nil {
			return err
		}
		name := man.Name
		if name == "" {
			name = "AstrLink Guard"
		}
		m := &modelInfo{id: man.ID + "-" + man.Variant, digest: "sha256:" + hexSum(raw), backend: man.Backend, name: name, version: man.Version,
			variant: strings.ToUpper(man.Variant), languages: man.Languages, repository: hubRepository(man.Repository),
			floor: floor, recommended: recommended, labels: man.LabelMap, categories: cats, declared: declared, plug: p, opt: o, kind: kind, entry: entry,
			minMemory: man.MinMemory}
		if m.languages == "" {
			m.languages = "中文 / 中英混排"
		}
		if o.Accel != nil {
			m.accel = readVariant(raw, o.Accel)
		}
		e.models = append(e.models, m)
	}
	return nil
}

func (e *Engine) registerPlugin(tx store.Tx, b Builtin) (plugin, error) {
	var pm registry.Manifest
	if json.Unmarshal(b.Manifest, &pm) != nil {
		return plugin{}, ErrOptions
	}
	digest := "sha256:" + hexSum(b.Manifest, []byte(e.opt.EnvDigest))
	p, err := e.reg.Register(tx, b.Manifest, digest, e.opt.EnvDigest, true)
	if errors.Is(err, registry.ErrConflict) {
		p, err = e.reg.Get(tx, registry.Identity{ID: pm.ID, Version: pm.Version, Digest: digest})
	}
	if err != nil {
		return plugin{}, err
	}
	if err = e.reg.Enable(tx, p.Identity, true); err != nil {
		return plugin{}, err
	}
	return plugin{module: b.Module, id: snapshot.PluginRef{ID: p.ID, Version: p.Version, Digest: p.Digest, Env: p.Environment}, man: p.Manifest}, nil
}

// hubRepoPattern is a Hugging Face model id: owner and name of letters, digits, '-', '_' and '.'.
var hubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}/[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

// hubRepository keeps the manifest's repository only when it is a plain model id, since the interface and the
// downloads turn it into an address; anything else is dropped.
func hubRepository(s string) string {
	if !hubRepoPattern.MatchString(s) || strings.Contains(s, "..") {
		return ""
	}
	return s
}

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// readVariant accepts the accelerated build only as another build of the same model with the same labels;
// anything else leaves the installation without acceleration rather than failing to open.
func readVariant(base []byte, v *ModelVariant) *variantInfo {
	raw, a, entry, err := readManifest(v.ManifestPath)
	if err != nil {
		return nil
	}
	var b modelManifest
	if json.Unmarshal(base, &b) != nil {
		return nil
	}
	labels := a.Files["labels.json"].SHA256
	if a.ID != b.ID || a.Version != b.Version || a.Variant == "" || a.Variant == b.Variant || deviceOf(a.Backend) != "gpu" ||
		labels == "" || labels != b.Files["labels.json"].SHA256 {
		return nil
	}
	return &variantInfo{id: a.ID + "-" + a.Variant, digest: "sha256:" + hexSum(raw), backend: a.Backend, dir: v.Dir, manifest: v.ManifestPath, entry: entry}
}

// source is where one downloadable build's files are on the hub.
type source struct {
	repository, revision, dir string
	paths                     map[string]string
}

// sourceOf reads where a manifest's files are on the hub: its repository at the pinned revision, in the
// folder named after the variant unless the manifest says otherwise.
func sourceOf(manifest string) (source, error) {
	_, man, _, err := readManifest(manifest)
	if err != nil {
		return source{}, err
	}
	repo := hubRepository(man.Repository)
	if repo == "" || !revisionPattern.MatchString(man.Revision) {
		return source{}, ErrOptions
	}
	dir := man.Variant
	if man.SourceDir != nil {
		dir = *man.SourceDir
	}
	for name := range man.SourcePaths {
		if _, ok := man.Files[name]; !ok {
			return source{}, ErrOptions // a path for a file the manifest does not list
		}
	}
	return source{repository: repo, revision: man.Revision, dir: dir, paths: man.SourcePaths}, nil
}

// url is the address of one file at the pinned revision: base/<repository>/resolve/<revision>/<path>.
func (s source) url(base, file string) (string, error) {
	path := s.paths[file]
	if path == "" {
		path = strings.TrimPrefix(s.dir+"/"+file, "/")
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", ErrOptions
		}
		parts[i] = url.PathEscape(p)
	}
	return base + "/" + s.repository + "/resolve/" + s.revision + "/" + strings.Join(parts, "/"), nil
}

// openCatalog starts the download service over the builds whose files are not preinstalled.
func (e *Engine) openCatalog(ctx context.Context) error {
	sources := map[string]source{}
	var entries []models.Entry
	add := func(path string, entry models.Entry) error {
		s, err := sourceOf(path)
		if err != nil {
			return err
		}
		sources[entry.Key()] = s
		entries = append(entries, entry)
		return nil
	}
	for _, m := range e.models {
		if m.opt.Dir == "" {
			if err := add(m.opt.ManifestPath, m.entry); err != nil {
				return err
			}
		}
		if v := m.accel; v != nil && v.dir == "" {
			if err := add(v.manifest, v.entry); err != nil {
				return err
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	root := e.opt.ModelsRoot
	if root == "" {
		root = filepath.Join(e.opt.Dir, "models")
	}
	src := e.opt.ModelSource
	if src == nil {
		src = &ModelSource{Base: "https://huggingface.co", Hosts: []string{"huggingface.co", ".hf.co"}}
	}
	svc, err := models.New(models.Config{
		Commit: e.ws.Commit, Root: root, Gate: e.gate, Hosts: src.Hosts, TLS: src.TLS, Catalog: entries,
		URL: func(entry models.Entry, file string) (string, error) {
			s, ok := sources[entry.Key()]
			if !ok {
				return "", ErrModelUnknown
			}
			return s.url(src.Base, file)
		},
		Free:    platform.FreeSpace,
		Prepare: e.prepareInstalled,
	})
	if err != nil {
		return err
	}
	if err = svc.Recover(ctx); err != nil {
		return err
	}
	e.catalog = svc
	return nil
}

// -- where the files are -----------------------------------------------------------------------------------

func (e *Engine) modelByID(id string) *modelInfo {
	for _, m := range e.models {
		if m.id == id {
			return m
		}
	}
	return nil
}

// buildDir is the directory of a build's verified files: preinstalled, or downloaded and still intact.
func (e *Engine) buildDir(ctx context.Context, preinstalled string, entry models.Entry) (string, error) {
	if preinstalled != "" {
		return preinstalled, nil
	}
	if e.catalog == nil {
		return "", ErrModelNotInstalled
	}
	b, err := e.catalog.CheckBinding(ctx, entry.ID, entry.Digest)
	if err != nil {
		return "", ErrModelNotInstalled
	}
	return b.Dir, nil
}

// available tells, per model and accelerated build id, whether its files are there now. It reads the
// catalog, so it is called outside a workspace transaction.
func (e *Engine) available(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, m := range e.models {
		if _, err := e.buildDir(ctx, m.opt.Dir, m.entry); err == nil {
			out[m.id] = true
		}
		if v := m.accel; v != nil {
			if _, err := e.buildDir(ctx, v.dir, v.entry); err == nil {
				out[v.id] = true
			}
		}
	}
	return out
}

func (e *Engine) memory() (uint64, error) {
	if e.opt.Memory != nil {
		return e.opt.Memory()
	}
	return platform.PhysicalMemory()
}

// eligible: the machine has the memory the model asks for (an unknown amount counts as enough only for a
// model that asks for none).
func (e *Engine) eligible(m *modelInfo) bool {
	if m.minMemory == 0 {
		return true
	}
	n, err := e.memory()
	return err == nil && n >= m.minMemory
}

// -- what the interface shows and asks ---------------------------------------------------------------------

// Models implements Core.
func (e *Engine) Models(ctx context.Context) ([]ModelRuntime, error) {
	accelOn := false
	if len(e.models) > 0 {
		err := e.ws.Commit(ctx, func(tx store.Tx) error {
			s, err := e.cfg.Settings(tx)
			accelOn = err == nil && s.GPUAcceleration
			return err
		})
		if err != nil {
			return nil, mapConfigErr(err)
		}
	}
	states := map[string]models.Status{}
	if e.catalog != nil {
		list, err := e.catalog.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, st := range list {
			states[st.Entry.Key()] = st
		}
	}
	out := make([]ModelRuntime, 0, len(e.models))
	for _, m := range e.models {
		rt := ModelRuntime{ID: m.id, Build: m.id, Name: m.name, Version: m.version, Variant: m.variant, Languages: m.languages, Backend: m.backend,
			Device: deviceOf(m.backend), Repository: m.repository, Preset: m.kind.presetID, Bytes: m.entry.Total(), MinMemory: m.minMemory,
			Eligible: e.eligible(m), Accelerable: m.accel != nil}
		if m.opt.Dir != "" {
			rt.State, rt.Downloaded, rt.Dir = string(models.StateReady), rt.Bytes, m.opt.Dir
		} else if st, ok := states[m.entry.Key()]; ok {
			rt.State, rt.Code, rt.Downloaded = string(st.State), st.Code, st.Downloaded
			if installedState(st.State) {
				rt.Dir = e.catalog.Dir(m.entry.Key())
			}
		}
		// Device selection never changes the build or its download state.
		rt.Build, rt.Backend = m.id, e.reportedBackend(m, accelOn)
		rt.Device = deviceOf(rt.Backend)
		rt.Accelerable, rt.Accelerated = deviceOf(m.backend) == "gpu", rt.Device == "gpu"
		rt.Accel = nil
		out = append(out, rt)
	}
	return out, nil
}

func installedState(s models.State) bool {
	return s == models.StateInstalled || s == models.StateReady || s == models.StatePreparing
}

// buildByID finds a downloadable build by id: a model of the catalog or a model's accelerated build. preinstalled:
// its files are given (nothing to download).
func (e *Engine) buildByID(id string) (m *modelInfo, key string, preinstalled bool, err error) {
	for _, x := range e.models {
		if x.id == id {
			return x, x.entry.Key(), x.opt.Dir != "", nil
		}
		if v := x.accel; v != nil && v.id == id {
			return x, v.entry.Key(), v.dir != "", nil
		}
	}
	return nil, "", false, ErrModelUnknown
}

// DownloadModel implements Core: it starts the download (then verification, installation and the trial run)
// of a model or of an accelerated build in the background and returns at once; Models reports the progress.
func (e *Engine) DownloadModel(ctx context.Context, id string) error {
	m, key, preinstalled, err := e.buildByID(id)
	switch {
	case err != nil:
		return err
	case preinstalled || e.catalog == nil:
		return ErrNotAllowed // nothing to download
	case !e.eligible(m):
		return ErrModelMemory
	}
	if err := e.networkFree(); err != nil {
		return err
	}
	go e.catalog.Download(e.life, key) // the outcome is the build's state
	return nil
}

// networkFree refuses a download while documents are processed (the gate would refuse it later anyway, but
// in the background where nobody would hear it).
func (e *Engine) networkFree() error {
	l, err := e.gate.Acquire(netgate.Network)
	if err != nil {
		return ErrModelBusy
	}
	l.Release()
	return nil
}

// PauseModel implements Core.
func (e *Engine) PauseModel(_ context.Context, id string) error {
	_, key, _, err := e.buildByID(id)
	if err != nil || e.catalog == nil {
		return ErrModelUnknown
	}
	return e.catalog.Pause(key)
}

// DiscardModel implements Core: the partial download is dropped, so the next download starts afresh.
func (e *Engine) DiscardModel(ctx context.Context, id string) error {
	_, key, _, err := e.buildByID(id)
	if err != nil || e.catalog == nil {
		return ErrModelUnknown
	}
	return e.catalog.Discard(ctx, key)
}

// PrepareModel implements Core: an installed model is loaded and runs its trial again, in the background.
func (e *Engine) PrepareModel(_ context.Context, id string) error {
	_, key, preinstalled, err := e.buildByID(id)
	if err != nil || e.catalog == nil || preinstalled {
		return ErrModelUnknown
	}
	go e.catalog.Prepare(e.life, key)
	return nil
}

// ModelDirectory implements Core: the folder of the files the model runs with now (the accelerated build's
// when acceleration is on), for the file manager.
func (e *Engine) ModelDirectory(ctx context.Context, id string) (string, error) {
	list, err := e.Models(ctx)
	if err != nil {
		return "", err
	}
	for i, m := range e.models { // Models lists them in this order
		if m.id == id && list[i].Dir != "" {
			return list[i].Dir, nil
		}
	}
	return "", ErrModelNotInstalled
}

// -- trial runs ------------------------------------------------------------------------------------------

// prepareInstalled is the catalog's preparation: the downloaded build is loaded by its plugin and runs the
// built-in sample; only then is it ready.
func (e *Engine) prepareInstalled(ctx context.Context, dir string, entry models.Entry) error {
	for _, m := range e.models {
		if m.entry.Key() == entry.Key() {
			s, err := e.SettingsGet(ctx)
			if err != nil {
				return err
			}
			backend := "CPUExecutionProvider"
			if s.GPUAcceleration && deviceOf(m.backend) == "gpu" {
				backend = "auto"
			}
			return e.trial(ctx, m, []string{"--model-dir", dir, "--manifest", m.opt.ManifestPath, "--backend", backend, "--cache-dir", e.modelCache(m.digest)}, accelStartTimeout, true)
		}
		if v := m.accel; v != nil && v.entry.Key() == entry.Key() {
			return e.trial(ctx, m, []string{"--model-dir", dir, "--manifest", v.manifest, "--cache-dir", e.accelCache(v)}, accelStartTimeout, true)
		}
	}
	return ErrModelUnknown
}

var trialSeq atomic.Uint64

// trial loads a build of m with args and runs its sample. wait: queue for the model slot instead of refusing.
func (e *Engine) trial(ctx context.Context, m *modelInfo, args []string, timeout time.Duration, wait bool) error {
	select {
	case e.modelSem <- struct{}{}:
	default:
		if !wait {
			return ErrModelBusy
		}
		select {
		case e.modelSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { <-e.modelSem }()
	w, err := e.launchWithin(ctx, m.plug, args, nil, timeout)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrTrialStart, err)
	}
	defer w.Terminate(time.Second)
	requested := "CPUExecutionProvider"
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--backend" {
			requested = args[i+1]
		}
	}
	e.recordBackend(m, requested, w.Session)
	n := trialSeq.Add(1)
	pm, err := detect.NewPluginModel(w.Session, func() kernel.AttemptRef {
		return kernel.AttemptRef{JobID: "model_trial", AttemptID: fmt.Sprintf("model_trial_%d", n), OwnerGeneration: e.ws.Generation(), JobVersion: 1}
	})
	if err != nil {
		return err
	}
	spans, err := pm.Run(ctx, m.kind.trialText)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrTrial, err)
	}
	if !trialPassed(m.kind, spans, m.recommended) {
		return ErrTrial
	}
	return nil
}

// trialPassed: every expected value is found with its label at or above the model's recommended score.
func trialPassed(k modelKind, spans []detect.ModelSpan, min float64) bool {
	runes := []rune(k.trialText)
	found := map[string]bool{}
	for _, s := range spans {
		if s.Start < 0 || s.End > len(runes) || s.Start >= s.End || s.Score < min {
			continue
		}
		if want, ok := k.trialWant[s.Label]; ok && string(runes[s.Start:s.End]) == want {
			found[s.Label] = true
		}
	}
	return len(found) == len(k.trialWant)
}

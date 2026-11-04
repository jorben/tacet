package gui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jorben/tacet/internal/app"
)

// Event names; they must stay in sync with src/services/wails.ts.
const (
	EventBatchChanged = "tacet:batch-changed" // payload: app.BatchStatus
	EventFilesDropped = "tacet:files-dropped" // payload: []string of absolute paths
)

// Info describes the running application and whether its engine is usable.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
	// Problem is the closed code that kept the engine from opening (for example
	// WORKSPACE_BUSY or KEYSTORE_UNAVAILABLE); empty when Ready.
	Problem string `json:"problem,omitempty"`
}

// Host is what the service needs from the native shell. The Wails adapter lives
// in cmd/tacet-desktop; tests use a fake.
type Host interface {
	PickFiles(title string) ([]string, error)
	PickFolder(title string) (string, error)
	OpenDirectory(path string) error
	// SaveText asks where to put a text file and writes it; false means the person cancelled.
	SaveText(title, suggested, text string) (bool, error)
	// PickText asks for a text file and returns its content; "" with ok=false means cancelled.
	PickText(title string, limit int64) (text string, ok bool, err error)
	Emit(event string, data any)
}

// Opener opens the shared core on first use.
type Opener func(ctx context.Context) (app.Core, error)

// Service is bound to the frontend. It exposes app commands and query reads
// only; plugin methods are never bound and no judgement lives here — status
// and verdicts come from the same core the CLI uses.
type Service struct {
	closeReady atomic.Bool
	ending     atomic.Bool // the system is logging out or shutting down

	open Opener
	host Host

	mu      sync.Mutex
	core    app.Core
	problem string
	unsub   func()
	dirty   map[string]bool
	flush   bool
	lastDur time.Duration
}

// NewService returns the bound service. A nil opener gives a service without
// an engine (the browser walkthrough and tests of the window behaviour).
func NewService(open Opener, host Host) *Service {
	return &Service{open: open, host: host, dirty: map[string]bool{}}
}

// AppInfo returns the product name, version and engine state.
func (s *Service) AppInfo() Info {
	info := Info{Name: app.Name, Version: app.Version}
	if _, err := s.engine(); err != nil {
		info.Problem = codeOf(err)
		return info
	}
	info.Ready = true
	return info
}

// CloseHandlerReady is called by the frontend once it listens for
// EventCloseRequested. Until then a user-initiated window close keeps its
// native behaviour, so a frontend that failed to load can never trap the window.
func (s *Service) CloseHandlerReady() { s.closeReady.Store(true) }

var errNoEngine = errors.New("ENGINE_UNAVAILABLE")

// codeOf names an error by its closed code; anything else is a generic failure
// so that paths and content never reach the frontend.
func codeOf(err error) string {
	msg := err.Error()
	for i := 0; i < len(msg); i++ {
		if c := msg[i]; c == ':' {
			msg = msg[:i]
			break
		}
	}
	if msg == "" {
		return "INTERNAL_ERROR"
	}
	for i := 0; i < len(msg); i++ {
		if c := msg[i]; !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "INTERNAL_ERROR"
		}
	}
	return msg
}

// publicError keeps the closed code and drops everything else.
func publicError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(codeOf(err))
}

func (s *Service) engine() (app.Core, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core != nil {
		return s.core, nil
	}
	if s.open == nil {
		return nil, errNoEngine
	}
	core, err := s.open(context.Background())
	if err != nil {
		s.problem = codeOf(err)
		return nil, err
	}
	s.core = core
	if n, ok := core.(app.Notifier); ok && s.host != nil {
		s.unsub = n.Subscribe(s.changed)
	}
	go s.refreshTray() // what survived the last run shows in the tray at once
	return core, nil
}

// Close ends the engine; the desktop entry calls it when the application quits.
// It is not bound: the frontend cannot reach it (see Shutdown).
func (s *Service) close() error {
	s.mu.Lock()
	core, unsub := s.core, s.unsub
	s.core, s.unsub = nil, nil
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if core == nil {
		return nil
	}
	if sc, ok := core.(app.SystemCloser); ok && s.ending.Load() {
		return sc.CloseForSystem()
	}
	return core.Close()
}

// changed coalesces change notifications: at most one status read per batch is
// in flight, and a slow read lengthens the pause before the next one.
func (s *Service) changed(batch string) {
	s.mu.Lock()
	s.dirty[batch] = true
	if s.flush {
		s.mu.Unlock()
		return
	}
	s.flush = true
	wait := 150*time.Millisecond + 4*s.lastDur
	s.mu.Unlock()
	time.AfterFunc(wait, s.flushDirty)
}

func (s *Service) flushDirty() {
	s.mu.Lock()
	batches := make([]string, 0, len(s.dirty))
	for b := range s.dirty {
		batches = append(batches, b)
	}
	s.dirty = map[string]bool{}
	core := s.core
	s.mu.Unlock()
	start := time.Now()
	if core != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		for _, b := range batches {
			if st, err := core.Status(ctx, b, 200); err == nil {
				s.host.Emit(EventBatchChanged, st)
			}
		}
		cancel()
	}
	s.refreshTray()
	s.mu.Lock()
	s.lastDur = time.Since(start)
	again := len(s.dirty) > 0
	if !again {
		s.flush = false
	}
	wait := 150*time.Millisecond + 4*s.lastDur
	s.mu.Unlock()
	if again {
		time.AfterFunc(wait, s.flushDirty)
	}
}

// -- native pickers ------------------------------------------------------------

// PickFiles opens the system file chooser and returns absolute paths.
func (s *Service) PickFiles() ([]string, error) {
	if s.host == nil {
		return nil, errNoEngine
	}
	files, err := s.host.PickFiles("选择待脱敏文档")
	return files, publicError(err)
}

// PickFolder opens the system folder chooser; an empty string means cancelled.
func (s *Service) PickFolder() (string, error) {
	if s.host == nil {
		return "", errNoEngine
	}
	dir, err := s.host.PickFolder("选择输出位置")
	return dir, publicError(err)
}

// DefaultOutputDir is the folder results go to unless the person chooses one.
func (s *Service) DefaultOutputDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("OUTPUT_UNAVAILABLE")
	}
	return filepath.Join(home, "Desktop", "Tacet 已脱敏"), nil
}

// WorkspaceInfo reports the active workspace's actual storage use.
func (s *Service) WorkspaceInfo() (app.WorkspaceInfo, error) {
	core, err := s.engine()
	if err != nil {
		return app.WorkspaceInfo{}, publicError(err)
	}
	info, err := core.WorkspaceInfo(context.Background())
	return info, publicError(err)
}

// OpenWorkspace opens only the active workspace, never a path supplied by the frontend.
func (s *Service) OpenWorkspace() error {
	if s.host == nil {
		return errNoEngine
	}
	info, err := s.WorkspaceInfo()
	if err != nil {
		return err
	}
	return publicError(s.host.OpenDirectory(info.Dir))
}

// -- commands ------------------------------------------------------------------

// ImportArgs describe the documents to take. Quick means the single-file path.
type ImportArgs struct {
	Files     []string `json:"files"`
	Quick     bool     `json:"quick"`
	Preset    string   `json:"preset"`
	OutputDir string   `json:"outputDir"`
}

// StartArgs confirm a prepared batch.
type StartArgs struct {
	Batch        string `json:"batch"`
	Preset       string `json:"preset"`
	OutputDir    string `json:"outputDir"`
	Authorize    bool   `json:"authorize"`
	AllowPartial bool   `json:"allowPartial"`
	Mode         string `json:"mode,omitempty"` // "guided" (default) or "strict"
}

// ImportFiles creates a draft batch and starts the read-only inspection.
func (s *Service) ImportFiles(a ImportArgs) (app.BatchStatus, error) {
	core, err := s.engine()
	if err != nil {
		return app.BatchStatus{}, publicError(err)
	}
	out := a.OutputDir
	if out == "" {
		if out, err = s.DefaultOutputDir(); err != nil {
			return app.BatchStatus{}, err
		}
		if err = os.MkdirAll(out, 0o700); err != nil {
			return app.BatchStatus{}, errors.New("OUTPUT_UNAVAILABLE")
		}
	}
	kind := "batch"
	if a.Quick {
		kind = "quick"
	}
	st, err := core.Import(context.Background(), app.ImportRequest{Files: a.Files, Kind: kind, Preset: a.Preset, OutputDir: out})
	return st, publicError(err)
}

// Start freezes the settings and runs the batch.
func (s *Service) Start(a StartArgs) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.Start(context.Background(), app.StartRequest{Batch: a.Batch, Preset: a.Preset, OutputDir: a.OutputDir, Authorize: a.Authorize, AllowPartial: a.AllowPartial, Mode: a.Mode}))
}

// Preflight returns once every file of a draft batch has been inspected.
func (s *Service) Preflight(batch string) (app.BatchStatus, error) {
	core, err := s.engine()
	if err != nil {
		return app.BatchStatus{}, publicError(err)
	}
	st, err := core.Preflight(context.Background(), batch)
	return st, publicError(err)
}

// BatchStatus is the one status read; the desktop and the CLI show the same.
func (s *Service) BatchStatus(batch string, limit int) (app.BatchStatus, error) {
	core, err := s.engine()
	if err != nil {
		return app.BatchStatus{}, publicError(err)
	}
	st, err := core.Status(context.Background(), batch, limit)
	return st, publicError(err)
}

// BatchFiles pages the rows of a batch (at most 200 per call).
func (s *Service) BatchFiles(batch string, offset, limit int) ([]app.FileStatus, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	fs, err := core.Files(context.Background(), batch, offset, limit)
	return fs, publicError(err)
}

func (s *Service) simple(batch string, f func(app.Core, context.Context, string) error) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(f(core, context.Background(), batch))
}

// PublishBatch authorizes publishing of everything that is ready, now or later, to outputDir
// (empty: the folder chosen so far).
func (s *Service) PublishBatch(batch string, partial bool, outputDir string) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.Publish(context.Background(), batch, partial, outputDir))
}

func (s *Service) PauseBatch(batch string) error {
	return s.simple(batch, func(c app.Core, ctx context.Context, b string) error { return c.Pause(ctx, b) })
}
func (s *Service) ResumeBatch(batch string) error {
	return s.simple(batch, func(c app.Core, ctx context.Context, b string) error { return c.Resume(ctx, b) })
}
func (s *Service) CancelBatch(batch string) error {
	return s.simple(batch, func(c app.Core, ctx context.Context, b string) error { return c.Cancel(ctx, b) })
}
func (s *Service) CleanBatch(batch string) error {
	return s.simple(batch, func(c app.Core, ctx context.Context, b string) error { return c.Clean(ctx, b) })
}

// RetryFiles re-runs failed or blocked files; no files means all of them.
func (s *Service) RetryFiles(batch string, files []string) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.Retry(context.Background(), batch, files))
}

// UnlockFile hands the password of a locked file to the engine. The password is
// not stored, logged or sent anywhere; the engine wipes its copy after use.
func (s *Service) UnlockFile(job, password string) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	pw := []byte(password)
	return publicError(core.Unlock(context.Background(), job, pw))
}

// -- review ---------------------------------------------------------------------

// ListFindings pages the candidates of a file, with the decision that applies.
func (s *Service) ListFindings(job string, after int64, limit int) ([]app.Finding, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	fs, err := core.Findings(context.Background(), job, after, limit)
	return fs, publicError(err)
}

// DocumentUnits pages the text units of a file with their candidates.
func (s *Service) DocumentUnits(job string, after, limit int) ([]app.DocUnit, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	us, err := core.Document(context.Background(), job, after, limit)
	return us, publicError(err)
}

// DecideFinding records the person's decision on one candidate (keep or redact).
func (s *Service) DecideFinding(job string, finding int64, keep bool) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.DecideFinding(context.Background(), job, finding, keep))
}

// DecideFindings records many decisions at once (a review's keep/redact choices).
// SkipFile leaves one waiting file out of its batch; it is not exported.
func (s *Service) SkipFile(job string) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.Skip(context.Background(), job))
}

// ConfirmFile confirms one file of a strict batch for export.
func (s *Service) ConfirmFile(job string) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.ConfirmFile(context.Background(), job))
}

func (s *Service) DecideFindings(job string, decisions []app.FindingDecision) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.DecideFindings(context.Background(), job, decisions))
}

// WithdrawManualRedaction undoes a manual redaction: the selection and its decision are taken back.
func (s *Service) WithdrawManualRedaction(job string, finding int64) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.WithdrawManual(context.Background(), job, finding))
}

// AddManualRedaction turns a selection of the document into a redacted candidate.
func (s *Service) AddManualRedaction(job string, seq, start, end int, category string) (int64, error) {
	core, err := s.engine()
	if err != nil {
		return 0, publicError(err)
	}
	id, err := core.AddManual(context.Background(), job, seq, start, end, category)
	return id, publicError(err)
}

// Shutdown ends the engine of svc. It is a function, not a method, so that Wails
// does not bind it to the frontend.
func Shutdown(svc *Service) error { return svc.close() }

// SystemEnding records that the system is logging out, restarting or shutting down: the exit that follows
// (Shutdown) takes the short order (architecture 6.2). Not bound: the frontend cannot call it.
func SystemEnding(svc *Service) { svc.ending.Store(true) }

// UnfinishedBatches lists the batches that are not finished, so the window can offer
// to continue them.
func (s *Service) UnfinishedBatches() ([]app.BatchStatus, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	bs, err := core.Unfinished(context.Background())
	return bs, publicError(err)
}

// -- configuration objects ---------------------------------------------------------

// ConfigList returns the objects of one kind (ruleset, scheme, preset, model_profile) with their content.
func (s *Service) ConfigList(kind string) ([]app.ConfigItem, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	items, err := core.ConfigList(context.Background(), kind)
	return items, publicError(err)
}

func (s *Service) ConfigCreate(kind, name, note, content string) (app.ConfigItem, error) {
	core, err := s.engine()
	if err != nil {
		return app.ConfigItem{}, publicError(err)
	}
	it, err := core.ConfigCreate(context.Background(), kind, name, note, content)
	return it, publicError(err)
}

func (s *Service) ConfigSave(id string, expected uint64, name, note, content string) (app.ConfigItem, error) {
	core, err := s.engine()
	if err != nil {
		return app.ConfigItem{}, publicError(err)
	}
	it, err := core.ConfigSave(context.Background(), id, expected, name, note, content)
	return it, publicError(err)
}

func (s *Service) ConfigCopy(id, name string) (app.ConfigItem, error) {
	core, err := s.engine()
	if err != nil {
		return app.ConfigItem{}, publicError(err)
	}
	it, err := core.ConfigCopy(context.Background(), id, name)
	return it, publicError(err)
}

func (s *Service) ConfigHistory(id string) ([]app.ConfigVersion, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	vs, err := core.ConfigHistory(context.Background(), id)
	return vs, publicError(err)
}

func (s *Service) ConfigDelete(id string, expected uint64) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(core.ConfigDelete(context.Background(), id, expected))
}

func (s *Service) ConfigRollback(id string, expected, restore uint64) (app.ConfigItem, error) {
	core, err := s.engine()
	if err != nil {
		return app.ConfigItem{}, publicError(err)
	}
	it, err := core.ConfigRollback(context.Background(), id, expected, restore)
	return it, publicError(err)
}

// presetTestTimeout bounds a preset test: loading the model, or waiting for the task that holds it.
const presetTestTimeout = 3 * time.Minute

// TestPreset runs the detection of a stored preset over a text the person typed; nothing is stored.
func (s *Service) TestPreset(preset, text string) (app.PresetTest, error) {
	core, err := s.engine()
	if err != nil {
		return app.PresetTest{}, publicError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), presetTestTimeout)
	defer cancel()
	res, err := core.TestPreset(ctx, preset, text)
	if err != nil && ctx.Err() != nil {
		return app.PresetTest{}, app.ErrTimeout
	}
	return res, publicError(err)
}

// MaxExchangeFile bounds a configuration file read from disk.
const MaxExchangeFile = 8 << 20

// SaveConfigFile saves an exchange file (a rule set or scheme) where the person chooses.
func (s *Service) SaveConfigFile(suggested, text string) (bool, error) {
	if s.host == nil {
		return false, errNoEngine
	}
	if len(text) == 0 || len(text) > MaxExchangeFile {
		return false, errors.New("INVALID_REQUEST")
	}
	ok, err := s.host.SaveText("导出", suggested, text)
	return ok, publicError(err)
}

// OpenConfigFile asks for an exchange file and returns its text; an empty string means cancelled.
func (s *Service) OpenConfigFile() (string, error) {
	if s.host == nil {
		return "", errNoEngine
	}
	text, ok, err := s.host.PickText("导入", MaxExchangeFile)
	if err != nil || !ok {
		return "", publicError(err)
	}
	return text, nil
}

// Settings returns the application preferences.
func (s *Service) Settings() (app.Settings, error) {
	core, err := s.engine()
	if err != nil {
		return app.Settings{}, publicError(err)
	}
	st, err := core.SettingsGet(context.Background())
	return st, publicError(err)
}

// SaveSettings stores the application preferences and returns what is stored.
func (s *Service) SaveSettings(in app.Settings) (app.Settings, error) {
	core, err := s.engine()
	if err != nil {
		return app.Settings{}, publicError(err)
	}
	st, err := core.SettingsSave(context.Background(), in)
	return st, publicError(err)
}

// Plugins lists the plugins this installation runs.
func (s *Service) Plugins() ([]app.PluginInfo, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	ps, err := core.Plugins(context.Background())
	return ps, publicError(err)
}

// Models lists the model catalog with each model's download state, where its files are and what it runs on.
func (s *Service) Models() ([]app.ModelRuntime, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	ms, err := core.Models(context.Background())
	return ms, publicError(err)
}

// Categories lists the category catalog in display order.
func (s *Service) Categories() ([]app.CategoryInfo, error) {
	core, err := s.engine()
	if err != nil {
		return nil, publicError(err)
	}
	cs, err := core.Categories(context.Background())
	return cs, publicError(err)
}

func (s *Service) model(id string, f func(app.Core, context.Context, string) error) error {
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	return publicError(f(core, context.Background(), id))
}

// DownloadModel starts downloading a model from its pinned source; verification, installation and the trial
// run follow in the background (Models reports the progress). Refused while documents are processed.
func (s *Service) DownloadModel(id string) error { return s.model(id, app.Core.DownloadModel) }

// PauseModel stops a download, keeping what was received.
func (s *Service) PauseModel(id string) error { return s.model(id, app.Core.PauseModel) }

// DiscardModel drops a partial download, so the next one starts afresh.
func (s *Service) DiscardModel(id string) error { return s.model(id, app.Core.DiscardModel) }

// PrepareModel loads an installed model and runs its trial again.
func (s *Service) PrepareModel(id string) error { return s.model(id, app.Core.PrepareModel) }

// OpenModelDirectory shows the folder of a model's files in the file manager. The folder is the one the
// engine reports for that model, never a path supplied by the frontend.
func (s *Service) OpenModelDirectory(id string) error {
	if s.host == nil {
		return errNoEngine
	}
	core, err := s.engine()
	if err != nil {
		return publicError(err)
	}
	dir, err := core.ModelDirectory(context.Background(), id)
	if err != nil {
		return publicError(err)
	}
	return publicError(s.host.OpenDirectory(dir))
}

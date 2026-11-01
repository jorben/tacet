package app

import (
	"context"
	"errors"
	"github.com/jorben/tacet/internal/extract"
)

// Public error codes of the use cases (module design 4.1). Messages carry no
// document content, paths or passwords (INV-07).
var (
	ErrNotFound   = errors.New("NOT_FOUND")
	ErrInvalid    = errors.New("INVALID_REQUEST")
	ErrNotAllowed = errors.New("ACTION_NOT_ALLOWED")
	ErrNotReady   = errors.New("NOT_READY")
	ErrVersion    = errors.New("VERSION_CONFLICT")
	ErrTimeout    = errors.New("TIMEOUT")
)

// ImportRequest names the files of a new batch. Kind "quick" is the
// single-file path (FR-28); at most one unfinished quick task and one regular
// batch exist at a time.
type ImportRequest struct {
	Files     []string // absolute paths
	Kind      string   // "quick" or "batch"
	Preset    string   // empty: the built-in preset
	OutputDir string   // absolute; where outputs are published
}

// FileStatus is the neutral, bounded description of one file. Name is the
// neutral identifier (f0001), never the source name.
type FileStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Status   string `json:"status"` // the verdict's result status
	Detail   string `json:"detail,omitempty"`
	Format   string `json:"format"`
	Pending  int    `json:"pending"`  // exceptions awaiting a decision
	Findings int    `json:"findings"` // detected candidates
	// Categories counts the candidates per rule category.
	Categories map[string]int `json:"categories,omitempty"`
	Output     string         `json:"output,omitempty"` // relative output name once published
	Digest     string         `json:"digest,omitempty"` // sha256 of the published file
	// Confirm is set while the file waits for its own confirmation (strict mode) before it is exported.
	Confirm bool `json:"confirm,omitempty"`
}

// BatchStatus is what the CLI prints and the desktop shows; both come from
// the same judgement (verdict), never from separate rules.
type BatchStatus struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Phase  string         `json:"phase"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	// Active counts files queued or executing, excluding paused and preflighted drafts.
	Active    int          `json:"active"`
	Delivered int          `json:"delivered"`
	ExitCode  int          `json:"exit_code"`
	Files     []FileStatus `json:"files"`
	Truncated bool         `json:"truncated,omitempty"`
	// Started: the settings were frozen (the batch was confirmed). Authorized: publishing was authorized.
	Started    bool `json:"started"`
	Authorized bool `json:"authorized"`
}

// StartRequest confirms a batch: the preset and the publish authorization.
type StartRequest struct {
	Batch        string
	Preset       string
	OutputDir    string
	Authorize    bool // publish everything that is eligible, per policy (FR-37)
	AllowPartial bool
	// Mode is how the batch is reviewed: "" or "guided" (the default), or "strict", where every file waits
	// for its own confirmation before it is exported (requirements 3.5.1). Unattended processing is not
	// offered: no category has the qualification it needs.
	Mode string `json:",omitempty"`
}

// Finding is one candidate, without its original value.
type Finding struct {
	ID       int64   `json:"id"`
	Unit     string  `json:"unit"`
	Category string  `json:"category"`
	Source   string  `json:"source"`
	Score    float64 `json:"score"`
	Start    int     `json:"start"`
	End      int     `json:"end"`
	Decision string  `json:"decision,omitempty"` // "redact", "keep" or empty
}

// DocFinding is one candidate inside a DocUnit. Start and End count UTF-16 code
// units of the unit's text, so a JavaScript front end can slice it directly.
type DocFinding struct {
	ID       int64   `json:"id"`
	Start    int     `json:"start"`
	End      int     `json:"end"`
	Category string  `json:"category"`
	Source   string  `json:"source"`
	Score    float64 `json:"score"`
	Low      bool    `json:"low"`
	Decision string  `json:"decision,omitempty"` // "redact", "keep" or empty
}

// DocUnit is one text unit of the document with the candidates found in it.
// The text is the person's own document shown on their own screen for review.
type DocUnit struct {
	Seq        int          `json:"seq"`
	Part       string       `json:"part"`
	Ordinal    int          `json:"ordinal"`
	Visibility string       `json:"visibility"`
	Text       string       `json:"text"`
	Findings   []DocFinding `json:"findings"`
	// Location is where the unit is (DOCX: approximate page, nearest heading, table cell); numbers only.
	Location *extract.Location `json:"location,omitempty"`
}

// FindingDecision is one person's decision on one candidate.
type FindingDecision struct {
	ID   int64 `json:"id"`
	Keep bool  `json:"keep"`
}

// PreviewBox is the place of one candidate on a rendered page: pixels of the image, left,
// top, right, bottom.
type PreviewBox struct {
	Finding  int64  `json:"finding"`
	Rect     [4]int `json:"rect"`
	Category string `json:"category"`
	Decision string `json:"decision,omitempty"`
}

// PagePreview is one rendered page. Image is a token for PreviewImage, valid while the
// engine keeps the page. Unmapped lists candidates that could not be placed reliably.
type PagePreview struct {
	Job      string       `json:"job"`
	Page     int          `json:"page"`
	Pages    int          `json:"pages"`
	Width    int          `json:"width"`
	Height   int          `json:"height"`
	Image    string       `json:"image"`
	Boxes    []PreviewBox `json:"boxes"`
	Unmapped []int64      `json:"unmapped"`
}

// ConfigItem is a stored configuration object (rule set, scheme, preset or model profile). Content
// is the object's JSON text; the validator of the config module judges it on every write.
type ConfigItem struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	Note               string `json:"note"`
	Version            uint64 `json:"version"`
	ReadOnly           bool   `json:"readOnly"`
	Content            string `json:"content"`
	MinConfidenceReset bool   `json:"minConfidenceReset,omitempty"`
}

// ConfigVersion is one of the last versions kept of an object.
type ConfigVersion struct {
	Number  uint64 `json:"number"`
	SavedAt int64  `json:"savedAt"` // unix seconds
}

// WorkspaceInfo describes the active workspace and its current file sizes.
type WorkspaceInfo struct {
	Dir   string `json:"dir"`
	Bytes int64  `json:"bytes"`
}

// Settings are the application preferences the settings page keeps.
type Settings struct {
	CloseBehavior string `json:"closeBehavior"` // tray or quit
	Theme         string `json:"theme"`         // system, light or dark
	RedactStyle   string `json:"redactStyle"`
	ResourceTier  string `json:"resourceTier"` // comfort or std
	RetentionDays int    `json:"retentionDays"`
	AutoUpdate    bool   `json:"autoUpdate"`
	// GPUAcceleration defaults on; CPU fallback keeps the same model build.
	GPUAcceleration bool `json:"gpuAcceleration"`
}

// PluginInfo is a registered plugin as the plugins page shows it.
type PluginInfo struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Roles   []string `json:"roles"`
	Formats []string `json:"formats"`
	Builtin bool     `json:"builtin"`
	Enabled bool     `json:"enabled"`
}

// ModelRuntime is one model of the catalog as the plugins page shows it: its download state, where its files
// are and what it runs on.
type ModelRuntime struct {
	ID        string `json:"id"`    // model id and variant, as in the model profile
	Build     string `json:"build"` // the installed build, independent of execution device
	Name      string `json:"name"`
	Version   string `json:"version"`
	Variant   string `json:"variant"`
	Languages string `json:"languages"`
	Preset    string `json:"preset"` // id of its built-in preset
	// State is the download state (internal/models): pending, downloading, paused, verifying, preparing,
	// installed, ready or error; Code says why for error.
	State      string `json:"state"`
	Code       string `json:"code"`
	Bytes      int64  `json:"bytes"`      // size of all its files
	Downloaded int64  `json:"downloaded"` // bytes received so far (all of them once installed)
	MinMemory  uint64 `json:"minMemory"`  // installed memory the machine needs; 0 = any
	Eligible   bool   `json:"eligible"`   // this machine has that memory
	Dir        string `json:"dir"`        // absolute directory of its files once installed, else ""
	Backend    string `json:"backend"`    // actual provider, or auto before the first load
	Device     string `json:"device"`     // cpu, gpu or auto (not loaded yet)
	// Repository is the model's Hugging Face id ("owner/name") from its integrity manifest; empty if unknown.
	Repository string `json:"repository"`
	// Accelerable identifies GPU-compatible weights; Accel is reserved for legacy clients.
	Accelerable bool         `json:"accelerable"`
	Accel       *BuildStatus `json:"accel"`
	// Accelerated means a worker confirmed a GPU provider for the current preference.
	Accelerated bool `json:"accelerated"`
}

// BuildStatus is the download state of one build of a model (the accelerated one).
type BuildStatus struct {
	ID         string `json:"id"`
	Variant    string `json:"variant"`
	Backend    string `json:"backend"`
	State      string `json:"state"` // as ModelRuntime.State
	Code       string `json:"code"`
	Bytes      int64  `json:"bytes"`
	Downloaded int64  `json:"downloaded"`
	Dir        string `json:"dir"`
}

// Core is the one interface the desktop app and the CLI use (AR-10). The
// engine implements it; nothing else reaches the kernel.
type Core interface {
	Import(ctx context.Context, r ImportRequest) (BatchStatus, error)
	// Preflight returns once every file of a draft batch has been inspected.
	Preflight(ctx context.Context, batch string) (BatchStatus, error)
	// Unfinished lists the batches that survived a restart (at most one quick and one regular),
	// paused where they were interrupted, with their rows.
	Unfinished(ctx context.Context) ([]BatchStatus, error)
	Start(ctx context.Context, r StartRequest) error
	Status(ctx context.Context, batch string, limit int) (BatchStatus, error)
	// Files pages the rows of a batch (at most 200) in import order.
	Files(ctx context.Context, batch string, offset, limit int) ([]FileStatus, error)
	// Wait returns when nothing in the batch is running or queued any more: every
	// file is finished or waiting for a person.
	Wait(ctx context.Context, batch string) (BatchStatus, error)
	// Unlock hands the password of a locked file to its next preflight. The slice is
	// wiped before Unlock returns; the password is never stored.
	Unlock(ctx context.Context, job string, password []byte) error
	Findings(ctx context.Context, job string, after int64, limit int) ([]Finding, error)
	// Document returns up to limit (at most 100) units after sequence number after,
	// with the candidates inside them; empty when the file has no extracted text yet.
	Document(ctx context.Context, job string, after, limit int) ([]DocUnit, error)
	// PagePreview renders page (1-based) of a PDF waiting for review at 72, 100 or 150 dpi.
	PagePreview(ctx context.Context, job string, page, dpi int) (PagePreview, error)
	DecideFinding(ctx context.Context, job string, finding int64, keep bool) error
	// DecideFindings records many decisions and redoes the file once.
	DecideFindings(ctx context.Context, job string, decisions []FindingDecision) error
	// ConfirmFile gives the per-file confirmation of strict mode; never for several files at once.
	ConfirmFile(ctx context.Context, job string) error
	// Skip leaves one file out of its batch: it is not exported (requirements 2.3, the one alternative to
	// the unblocking action of a whole-file block). Only a file that waits for the person may be skipped.
	Skip(ctx context.Context, job string) error
	// AddManual makes the text between start and end (UTF-16 code units of the unit
	// with sequence number seq) a candidate of the category and redacts it.
	AddManual(ctx context.Context, job string, seq, start, end int, category string) (int64, error)
	// WithdrawManual undoes a manual redaction (FR-22): the selection and its decision are taken back, and
	// the file is resolved again without it. Only a finding AddManual returned can be withdrawn.
	WithdrawManual(ctx context.Context, job string, finding int64) error
	// Publish authorizes publishing every file that is ready and every file that
	// becomes ready later (FR-37). partial also authorizes marked partial results.
	// outputDir, when set, names the folder results are written to; the location is
	// part of the authorization, so it can still be chosen here (FR-37).
	Publish(ctx context.Context, batch string, partial bool, outputDir string) error
	Pause(ctx context.Context, batch string) error
	// Configuration objects. Writes check the version the caller saw (ErrVersion on a clash) and the
	// object's content (ErrInvalid); built-in objects are read-only (ErrNotAllowed).
	ConfigList(ctx context.Context, kind string) ([]ConfigItem, error)
	ConfigCreate(ctx context.Context, kind, name, note, content string) (ConfigItem, error)
	ConfigSave(ctx context.Context, id string, expected uint64, name, note, content string) (ConfigItem, error)
	ConfigCopy(ctx context.Context, id, name string) (ConfigItem, error)
	ConfigHistory(ctx context.Context, id string) ([]ConfigVersion, error)
	// ConfigDelete removes a user object that no preset refers to.
	ConfigDelete(ctx context.Context, id string, expected uint64) error
	// Plugins lists the plugins this installation runs: the built-in format and detection plugins.
	Plugins(ctx context.Context) ([]PluginInfo, error)
	// Models lists the model catalog with each model's download state.
	Models(ctx context.Context) ([]ModelRuntime, error)
	// DownloadModel starts downloading a model (verification, installation and trial follow) and returns;
	// PauseModel stops it keeping what was received; DiscardModel drops a partial download; PrepareModel
	// runs an installed model's trial again.
	DownloadModel(ctx context.Context, id string) error
	PauseModel(ctx context.Context, id string) error
	DiscardModel(ctx context.Context, id string) error
	PrepareModel(ctx context.Context, id string) error
	// ModelDirectory is the folder of the files a model runs with now.
	ModelDirectory(ctx context.Context, id string) (string, error)
	WorkspaceInfo(ctx context.Context) (WorkspaceInfo, error)
	SettingsGet(ctx context.Context) (Settings, error)
	SettingsSave(ctx context.Context, s Settings) (Settings, error)
	ConfigRollback(ctx context.Context, id string, expected, restore uint64) (ConfigItem, error)
	Resume(ctx context.Context, batch string) error
	Cancel(ctx context.Context, batch string) error
	Retry(ctx context.Context, batch string, jobs []string) error
	Clean(ctx context.Context, batch string) error
	Close() error
}

// Notifier is implemented by a core that can say when a batch changed, so a
// front end shows real progress without polling. The callback must not block.
type Notifier interface {
	Subscribe(fn func(batch string)) (cancel func())
}

// SystemCloser closes for a system shutdown or logout: no waiting, no cleaning (architecture 6.2).
type SystemCloser interface {
	CloseForSystem() error
}

// Waker is told that the system woke from sleep (architecture 6.2): the deadlines of the running plugin
// calls take the moment of waking as their baseline, so the time asleep is not counted against them.
type Waker interface {
	Woke(ctx context.Context) error
}

// PreviewSource serves the rendered pages PagePreview announced.
type PreviewSource interface {
	PreviewImage(token string) ([]byte, bool)
}

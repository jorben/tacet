package app

import (
	"context"
	"errors"
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
}

// BatchStatus is what the CLI prints and the desktop shows; both come from
// the same judgement (verdict), never from separate rules.
type BatchStatus struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Phase     string         `json:"phase"`
	Total     int            `json:"total"`
	Counts    map[string]int `json:"counts"`
	Delivered int            `json:"delivered"`
	ExitCode  int            `json:"exit_code"`
	Files     []FileStatus   `json:"files"`
	Truncated bool           `json:"truncated,omitempty"`
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

// Settings are the application preferences the settings page keeps.
type Settings struct {
	CloseBehavior string `json:"closeBehavior"` // tray or quit
	Theme         string `json:"theme"`         // system, light or dark
	RedactStyle   string `json:"redactStyle"`
	ResourceTier  string `json:"resourceTier"` // comfort or std
	RetentionDays int    `json:"retentionDays"`
	AutoUpdate    bool   `json:"autoUpdate"`
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
	// AddManual makes the text between start and end (UTF-16 code units of the unit
	// with sequence number seq) a candidate of the category and redacts it.
	AddManual(ctx context.Context, job string, seq, start, end int, category string) (int64, error)
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

// PreviewSource serves the rendered pages PagePreview announced.
type PreviewSource interface {
	PreviewImage(token string) ([]byte, bool)
}

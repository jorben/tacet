package kernel

import "time"

// ArtifactKind names the kind of a stage product, such as a text unit set,
// a redaction plan or a candidate output.
type ArtifactKind string

// ArtifactRef identifies one version of an artifact. Modules pass these
// instead of large objects or shared mutable structures (MD-04).
type ArtifactRef struct {
	Kind    ArtifactKind
	ID      string
	Version uint64
	Digest  string
}

// Span is a 0-based half-open range [Start, End) of Unicode code points in
// one version of a text unit (architecture 5).
type Span struct {
	UnitID      string
	TextVersion uint64
	Start       int
	End         int
}

// Valid reports whether the range is well formed.
func (s Span) Valid() bool { return s.UnitID != "" && s.Start >= 0 && s.Start <= s.End }

// SourceLocator is a format-specific internal location (XML node, page
// coordinates, cell). It is opaque to the kernel and only stored and passed
// back to format plugins and plan geometry.
type SourceLocator struct {
	Format string
	Data   []byte
}

// DisplayLocator is the user-facing location used for navigation and
// explanations. It never drives write-back.
type DisplayLocator struct {
	Page       int  // 1-based; 0 when unknown
	PageApprox bool // DOCX pages come from saved page-break hints
	Section    string
	Paragraph  int
	Sheet      string
	Cell       string
	Line       int
	Area       string // header, footnote, comment, text box ...
}

// AttemptRef binds a stage result to the attempt that produced it. Results
// whose generation, attempt or job version no longer match are dropped.
type AttemptRef struct {
	JobID           string
	AttemptID       string
	OwnerGeneration uint64
	JobVersion      uint64
}

// RetryClass says whether an error may be retried and how.
type RetryClass string

const (
	RetryNone      RetryClass = "none"
	RetryResource  RetryClass = "resource"  // retried once the resource condition clears
	RetryTransient RetryClass = "transient" // retried within the automatic budget
)

// ErrorCode is a stable error code with redacted parameters. Params must
// never carry original values, document fragments, full paths or passwords
// (INV-07).
type ErrorCode struct {
	Code   string
	Retry  RetryClass
	Params map[string]string
}

func (e *ErrorCode) Error() string { return e.Code }

// Priority is the scheduling level of a stage.
type Priority string

const (
	Background  Priority = "background"
	Interactive Priority = "interactive" // quick tasks and files re-exported during review (AR-16)
)

// ResourceClaim is what a stage asks the scheduler to admit.
type ResourceClaim struct {
	MemoryBytes    int64
	CPUSlots       int
	WorkerKind     string
	ScratchBytes   int64
	CandidateBytes int64
	QueueBytes     int64
	Priority       Priority
}

// MonoTime is a reading of a monotonic clock that excludes sleep. Deadlines
// and heartbeats use it only (architecture 6.2).
type MonoTime time.Duration

// StageKind names a stage of the file pipeline.
type StageKind string

const (
	StageNone           StageKind = ""
	StagePreflight      StageKind = "preflight"
	StageUnlock         StageKind = "unlock"
	StageExtractDetect  StageKind = "extract_detect"
	StageSupplementScan StageKind = "supplement_scan"
	StageResolve        StageKind = "resolve"
	StageExport         StageKind = "export"
	StageValidate       StageKind = "validate"
	StagePublish        StageKind = "publish"
)

// Facet is a part of the run snapshot read by a set of stages. Each facet
// has its own digest so staleness is judged per facet (MD-09).
type Facet string

const (
	FacetDetect   Facet = "detect"   // models, rules, enabled categories, detection mode
	FacetDispose  Facet = "dispose"  // actions, minimum confidence, keep exceptions, default handling, review requirements
	FacetRender   Facet = "render"   // redaction style, placeholder forms, output parameters
	FacetRuntime  Facet = "runtime"  // plugin, environment and protocol versions, completion grants
	FacetDelivery Facet = "delivery" // output location, naming, publish grant, retention
)

// FindingSource is where a finding came from.
type FindingSource string

const (
	SourceRule       FindingSource = "rule"
	SourceDictionary FindingSource = "dictionary"
	SourceModel      FindingSource = "model"
	SourceFieldRule  FindingSource = "field_rule"
	SourceManual     FindingSource = "manual"
	SourceUserTerm   FindingSource = "user_term" // batch supplement terms (MD-18)
)

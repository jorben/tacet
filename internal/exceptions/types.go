package exceptions

// Type is an exception type code from the versioned type table
// (architecture 5.2). Unknown codes reported by plugins are protocol errors.
type Type string

const (
	PasswordRequired   Type = "password_required"
	FileFailed         Type = "file_failed"
	StrictConfirm      Type = "strict_confirm"
	ScanPage           Type = "scan_page"
	ImageWithText      Type = "image_with_text"
	EmbeddedObject     Type = "embedded_object"
	UninspectedPart    Type = "uninspected_part"
	OutlinedText       Type = "outlined_text"
	ValidatorMismatch  Type = "validator_mismatch"
	TypeChange         Type = "type_change"
	UnreliableLocation Type = "unreliable_location"
	PolicyReview       Type = "policy_review"
)

// Level says whether an exception concerns a whole file or some content.
type Level string

const (
	FileLevel    Level = "file"
	ContentLevel Level = "content"
)

// Grouping says whether an exception may be decided for a whole group.
type Grouping string

const (
	Groupable Grouping = "yes"
	PerItem   Grouping = "no"
	ByPolicy  Grouping = "policy" // decided by the frozen policy
)

// Info is one row of the type table.
type Info struct {
	Level Level
	Group Grouping
	// KeepIsPartial: choosing keep makes a partial result, so keep is only
	// offered when partial-result eligibility holds (MD-17).
	KeepIsPartial bool
	// PluginReportable: format plugins may report this code; the others are
	// produced by the kernel.
	PluginReportable bool
}

// Table is the exception type table.
var Table = map[Type]Info{
	PasswordRequired:   {Level: FileLevel, Group: Groupable},
	FileFailed:         {Level: FileLevel, Group: Groupable},
	StrictConfirm:      {Level: FileLevel, Group: PerItem},
	ScanPage:           {Level: ContentLevel, Group: Groupable, KeepIsPartial: true, PluginReportable: true},
	ImageWithText:      {Level: ContentLevel, Group: Groupable, KeepIsPartial: true, PluginReportable: true},
	EmbeddedObject:     {Level: ContentLevel, Group: Groupable, KeepIsPartial: true, PluginReportable: true},
	UninspectedPart:    {Level: ContentLevel, Group: Groupable, KeepIsPartial: true, PluginReportable: true},
	OutlinedText:       {Level: ContentLevel, Group: Groupable, PluginReportable: true},
	ValidatorMismatch:  {Level: ContentLevel, Group: Groupable},
	TypeChange:         {Level: ContentLevel, Group: Groupable},
	UnreliableLocation: {Level: ContentLevel, Group: PerItem},
	PolicyReview:       {Level: ContentLevel, Group: ByPolicy},
}

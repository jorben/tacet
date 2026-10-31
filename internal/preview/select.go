package preview

import (
	"errors"
	"strings"
	"unicode"
)

var (
	ErrStale       = errors.New("SELECTION_STALE")        // the unit's text version moved on
	ErrOutOfRange  = errors.New("SELECTION_OUT_OF_RANGE") // outside the unit's text
	ErrDenied      = errors.New("SELECTION_DENIED")       // the unit belongs to another task
	ErrNotFound    = errors.New("SELECTION_UNIT_UNKNOWN")
	ErrUnreliable  = errors.New("SELECTION_LOCATION_UNRELIABLE") // goes to the unreliable_location path
	ErrInvalid     = errors.New("SELECTION_INVALID")
	ErrMapperFails = errors.New("SELECTION_MAPPING_FAILED")
)

// Selection is what the interface sends: identifiers, a version and either a text
// range or viewport coordinates. It carries no text and no path (architecture 9).
type Selection struct {
	Job         string
	Unit        string
	TextVersion uint64
	// Text range, in code points of the unit text. Used when Coords is nil.
	Start, End int
	// Coords is a rectangle in the viewport's coordinates, mapped to a range by
	// the format plugin's selection map.
	Coords *Rect
}

// Rect is a viewport rectangle.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// Facts are what the host knows about the unit now.
type Facts struct {
	Job         string
	TextVersion uint64
	Chars       int
	Part        string
	Ordinal     int
	Reliable    bool // the unit's source map can address positions inside it (INV-09)
}

// Lookup returns the current facts of a unit.
type Lookup func(unit string) (Facts, bool)

// Mapper turns viewport coordinates into a code-point range of the unit, as the
// plugin's selection map does. Its answer is checked like any other range.
type Mapper func(unit string, r Rect) (start, end int, err error)

// Range is a validated selection, addressed by the unit and its source position.
type Range struct {
	Unit       string
	Start, End int
	Part       string
	Ordinal    int
}

// Validate checks a selection against the current facts and returns the range.
// A selection made against an older text version is refused, never remapped, so
// a stale view cannot redact the wrong characters; a unit whose positions cannot
// be addressed reliably returns ErrUnreliable (module design 7.5).
func Validate(sel Selection, look Lookup, mapper Mapper) (Range, error) {
	if sel.Job == "" || sel.Unit == "" || sel.TextVersion == 0 || look == nil {
		return Range{}, ErrInvalid
	}
	f, ok := look(sel.Unit)
	switch {
	case !ok:
		return Range{}, ErrNotFound
	case f.Job != sel.Job:
		return Range{}, ErrDenied // do not tell whether the unit exists elsewhere
	case f.TextVersion != sel.TextVersion:
		return Range{}, ErrStale
	}
	start, end := sel.Start, sel.End
	if sel.Coords != nil {
		if mapper == nil {
			return Range{}, ErrMapperFails
		}
		c := *sel.Coords
		if !(c.X0 < c.X1 && c.Y0 < c.Y1) {
			return Range{}, ErrInvalid
		}
		var err error
		if start, end, err = mapper(sel.Unit, c); err != nil {
			return Range{}, ErrMapperFails
		}
	}
	switch {
	case sel.Coords == nil && (start < 0 || start >= end):
		return Range{}, ErrInvalid
	case start < 0 || start >= end || end > f.Chars:
		return Range{}, ErrOutOfRange // also a mapper that answered outside the unit
	}
	if !f.Reliable {
		return Range{}, ErrUnreliable
	}
	return Range{Unit: sel.Unit, Start: start, End: end, Part: f.Part, Ordinal: f.Ordinal}, nil
}

// MaxText bounds the text returned to the interface in one piece.
const MaxText = 64 << 10

// SafeText prepares text for the interface: plain text only. Control characters
// (other than newline and tab) and the bidirectional overrides that can make text
// read differently from what it is are dropped, and the result is cut to MaxText code points. Markup is not
// interpreted here; the interface renders this as text, never as HTML.
func SafeText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= MaxText {
			break
		}
		switch {
		case r == '\n' || r == '\t':
		case unicode.IsControl(r):
			continue
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E || r == 0x200F:
			continue
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

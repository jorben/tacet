package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	ErrEncode          = errors.New("PLAN_ENCODE_FAILED")
	ErrUnsupportedKind = errors.New("PLAN_OPERATION_UNSUPPORTED")
)

// MaxReplacementRunes is the longest replacement the protocol carries.
const MaxReplacementRunes = 1024

// Locator names a unit's source position the way the format plugins' source
// maps do: the part and the ordinal inside it. The kernel only builds and
// splits it; the plugin gives it meaning.
func Locator(part string, ordinal int) string { return part + "#" + strconv.Itoa(ordinal) }

// ParseLocator is the inverse of Locator.
func ParseLocator(s string) (part string, ordinal int, ok bool) {
	i := strings.LastIndexByte(s, '#')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return s[:i], n, true
}

type line struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Part    string `json:"part"`
	Ordinal int    `json:"ordinal,omitempty"`
	Start   *int   `json:"start,omitempty"`
	End     *int   `json:"end,omitempty"`
	// A text operation always carries its replacement, empty for a deletion: the
	// protocol schema requires the field, so a pointer keeps "" from being omitted.
	Replacement *string `json:"replacement,omitempty"`
}

// Encode renders the plan as the JSON Lines resource the format plugin reads
// (format.schema PlanLine), one operation per line in plan order. replacement
// supplies the replacement text of a text operation (the host knows the render
// facet and the original value; the plan does not). A text operation needs
// exactly one reliable source fragment: more is a defect, not a guess. Region
// operations have no plugin counterpart yet and are refused.
func Encode(p Plan, replacement func(Operation) (string, error)) ([]byte, error) {
	var out bytes.Buffer
	for _, o := range p.Operations {
		var l line
		switch o.Spec.Kind {
		case TextOp:
			if len(o.Spec.Source) != 1 {
				return nil, ErrNoReliableSource
			}
			part, ordinal, ok := ParseLocator(o.Spec.Source[0].Locator)
			if !ok {
				return nil, ErrNoReliableSource
			}
			text, err := replacement(o)
			if err != nil || !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxReplacementRunes {
				return nil, ErrEncode
			}
			start, end := o.Spec.Start, o.Spec.End
			l = line{Kind: "text", ID: o.ID, Part: part, Ordinal: ordinal, Start: &start, End: &end, Replacement: &text}
		case PartOp:
			l = line{Kind: "part", ID: o.ID, Part: o.Spec.Unit}
		default:
			return nil, ErrUnsupportedKind
		}
		b, err := json.Marshal(l)
		if err != nil {
			return nil, ErrEncode
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// Renderer builds replacement text from the render facet (requirements 3.3).
type Renderer struct {
	// Placeholders maps a category to its placeholder text; Fallback is used for
	// a category without one.
	Placeholders map[string]string
	Fallback     string
	// A mask keeps this many code points at the start and at the end and covers the rest.
	MaskHead, MaskTail int
	MaskChar           rune
	// MinCovered is the least number of code points a partial mask must cover;
	// a value that would leave fewer is masked completely. Zero means 3.
	MinCovered int
}

// Text returns the replacement for an action on an original value. A value that
// would leave fewer than MinCovered code points covered is masked completely, so
// a short secret is not revealed by a mask that keeps most of it.
func (r Renderer) Text(action, category, original string) (string, error) {
	switch action {
	case "delete":
		return "", nil
	case "placeholder":
		if t, ok := r.Placeholders[category]; ok && t != "" {
			return t, nil
		}
		if r.Fallback == "" {
			return "", ErrEncode
		}
		return r.Fallback, nil
	case "mask":
		mc := r.MaskChar
		if mc == 0 || r.MaskHead < 0 || r.MaskTail < 0 || !utf8.ValidString(original) {
			return "", ErrEncode
		}
		runes := []rune(original)
		if len(runes) == 0 {
			return "", ErrEncode
		}
		need := r.MinCovered
		if need <= 0 {
			need = 3
		}
		if len(runes)-r.MaskHead-r.MaskTail < need {
			return strings.Repeat(string(mc), len(runes)), nil
		}
		return string(runes[:r.MaskHead]) + strings.Repeat(string(mc), len(runes)-r.MaskHead-r.MaskTail) + string(runes[len(runes)-r.MaskTail:]), nil
	}
	return "", ErrEncode
}

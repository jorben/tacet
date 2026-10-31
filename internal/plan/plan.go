package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PlannerVersion is part of the plan version; bump it when Build or Widen change.
const PlannerVersion = "1"

var (
	ErrNoReliableSource = errors.New("PLAN_SOURCE_UNRELIABLE")
	ErrOverlap          = errors.New("PLAN_OPERATIONS_OVERLAP")
	ErrInvalidOperation = errors.New("PLAN_OPERATION_INVALID")
	ErrWidenOutOfScope  = errors.New("PLAN_WIDEN_OUT_OF_SCOPE")
)

// Kind is the operation family (module design 7.4).
type Kind string

const (
	TextOp   Kind = "text"   // unit range with delete / placeholder / mask
	RegionOp Kind = "region" // page rectangle with a redaction style
	PartOp   Kind = "part"   // whole-part removal or neutral replacement
)

// Segment is one reliable source fragment of a text operation. The kernel does
// not interpret Locator; it comes from the format plugin's source map.
type Segment struct{ Locator string }

// Spec is the planner input derived from a disposition (policy output).
type Spec struct {
	Kind   Kind
	Unit   string // text unit, page, or part id
	Start  int    // code points, text operations only
	End    int
	Action string // delete, placeholder, mask, remove_part ...
	// Category is the finding category, which selects the placeholder text.
	Category string
	Rect     *Rect // region operations
	// Source must list at least one reliable fragment for text operations;
	// an empty source is a defect, never guessed from offsets (INV-09).
	Source []Segment
	// Container is the whole unit/field/image area, used only by Widen.
	ContainerEnd  int
	ContainerRect *Rect
}

// Operation is one planned, individually identified change.
type Operation struct {
	ID      string
	Spec    Spec
	Widened bool
}

// Plan is deterministic for a given input version, resolution and render facet.
type Plan struct {
	Version    string
	Operations []Operation
	// Patches counts automatic widenings; part of the version (INV-04).
	Patches int
}

func stableID(input string, s Spec) string {
	rect := ""
	if s.Rect != nil {
		rect = fmt.Sprintf("%d,%d,%d,%d", s.Rect.L, s.Rect.T, s.Rect.R, s.Rect.B)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s", input, s.Kind, s.Unit, s.Start, s.End, rect, s.Action)))
	return "op_" + hex.EncodeToString(sum[:10])
}

func version(resolutionKey, renderDigest string, ops []Operation, patches int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00", resolutionKey, renderDigest, PlannerVersion, patches)
	for _, o := range ops {
		fmt.Fprintf(h, "%s\x00", o.ID)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func validate(s Spec) error {
	switch s.Kind {
	case TextOp:
		if s.Unit == "" || s.Start < 0 || s.End <= s.Start {
			return ErrInvalidOperation
		}
		if len(s.Source) == 0 {
			return ErrNoReliableSource
		}
		for _, seg := range s.Source {
			if seg.Locator == "" {
				return ErrNoReliableSource
			}
		}
	case RegionOp:
		if s.Unit == "" || s.Rect == nil || s.Rect.R <= s.Rect.L || s.Rect.B <= s.Rect.T || s.Rect.L < 0 || s.Rect.T < 0 {
			return ErrInvalidOperation
		}
	case PartOp:
		if s.Unit == "" {
			return ErrInvalidOperation
		}
	default:
		return ErrInvalidOperation
	}
	if s.Action == "" {
		return ErrInvalidOperation
	}
	return nil
}

// Build creates the plan for one input. Operations in one unit must not
// overlap: policy has already merged them, so an overlap here is a bug that
// would double-apply replacement text.
func Build(inputVersion, resolutionKey, renderDigest string, specs []Spec) (Plan, error) {
	ops := make([]Operation, 0, len(specs))
	for _, s := range specs {
		if err := validate(s); err != nil {
			return Plan{}, err
		}
		ops = append(ops, Operation{ID: stableID(inputVersion, s), Spec: s})
	}
	if err := checkOverlap(ops); err != nil {
		return Plan{}, err
	}
	sortOps(ops)
	return Plan{Version: version(resolutionKey, renderDigest, ops, 0), Operations: ops}, nil
}

func sortOps(ops []Operation) {
	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i].Spec, ops[j].Spec
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return ops[i].ID < ops[j].ID
	})
}

func checkOverlap(ops []Operation) error {
	byUnit := map[string][]Spec{}
	seen := map[string]bool{}
	for _, o := range ops {
		if seen[o.ID] {
			return ErrOverlap // identical operation twice
		}
		seen[o.ID] = true
		if o.Spec.Kind == TextOp {
			byUnit[o.Spec.Unit] = append(byUnit[o.Spec.Unit], o.Spec)
		}
	}
	for _, specs := range byUnit {
		sort.Slice(specs, func(i, j int) bool { return specs[i].Start < specs[j].Start })
		for i := 1; i < len(specs); i++ {
			if specs[i].Start < specs[i-1].End {
				return ErrOverlap
			}
		}
	}
	return nil
}

// MaxPatches is how many automatic widenings a file's plan may carry: one
// (requirements 3.4). jobs counts it too; this refuses a second one even if a
// caller forgets to.
const MaxPatches = 1

// Residual is a place where an approved operation left content behind.
type Residual struct {
	Kind Kind
	Unit string
}

// Widen enlarges, once, each operation whose unit, field or image area still
// holds a residual: the operation grows to the whole container. A residual in
// a place with no approved operation, or a container that is unknown, returns
// ErrWidenOutOfScope and the caller must fail the file (3.4). The input plan is
// not modified; the result is a new plan version with the patch counted.
func Widen(p Plan, inputVersion, resolutionKey, renderDigest string, residuals []Residual) (Plan, error) {
	if len(residuals) == 0 || p.Patches >= MaxPatches {
		return Plan{}, ErrWidenOutOfScope
	}
	hit := map[string]bool{}
	for _, r := range residuals {
		found := false
		for _, o := range p.Operations {
			if o.Spec.Kind == r.Kind && o.Spec.Unit == r.Unit {
				found = true
			}
		}
		if !found {
			return Plan{}, ErrWidenOutOfScope
		}
		hit[string(r.Kind)+"\x00"+r.Unit] = true
	}
	var out []Operation
	done := map[string]bool{}
	for _, o := range p.Operations {
		key := string(o.Spec.Kind) + "\x00" + o.Spec.Unit
		if !hit[key] {
			out = append(out, o)
			continue
		}
		if done[key] {
			continue // folded into the widened operation
		}
		done[key] = true
		s := o.Spec
		switch s.Kind {
		case TextOp:
			if s.ContainerEnd <= 0 {
				return Plan{}, ErrWidenOutOfScope
			}
			s.Start, s.End = 0, s.ContainerEnd
			// Sources of all folded operations stay attached.
			s.Source = nil
			for _, other := range p.Operations {
				if other.Spec.Kind == TextOp && other.Spec.Unit == s.Unit {
					s.Source = append(s.Source, other.Spec.Source...)
				}
			}
			// Replacement strength: the strongest action in the unit.
			s.Action = strongest(p.Operations, s.Unit, s.Action)
		case RegionOp:
			if s.ContainerRect == nil {
				return Plan{}, ErrWidenOutOfScope
			}
			r := *s.ContainerRect
			s.Rect = &r
		default:
			return Plan{}, ErrWidenOutOfScope // a whole part is already maximal
		}
		out = append(out, Operation{ID: stableID(inputVersion, s), Spec: s, Widened: true})
	}
	sortOps(out)
	patches := p.Patches + 1
	return Plan{Version: version(resolutionKey, renderDigest, out, patches), Operations: out, Patches: patches}, nil
}

func strongest(ops []Operation, unit, fallback string) string {
	order := map[string]int{"mask": 1, "placeholder": 2, "delete": 3}
	best := fallback
	for _, o := range ops {
		if o.Spec.Kind == TextOp && o.Spec.Unit == unit && order[strings.ToLower(o.Spec.Action)] > order[best] {
			best = o.Spec.Action
		}
	}
	return best
}

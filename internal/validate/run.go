package validate

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jorben/tacet/internal/plan"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

var (
	ErrProtocol = errors.New("VALIDATE_PROTOCOL_VIOLATION")
	ErrInvalid  = errors.New("VALIDATE_INVALID")
)

// Check is the plugin's observation of one approved operation in the candidate.
type Check struct {
	Executed bool
	Residual bool
	Unit     string
}

// Report is what format.validate said, already checked for shape. It carries
// facts only; reconciliation, re-detection and the verdict are the kernel's.
type Report struct {
	InputParts  []coverage.InputPart
	OutputParts []coverage.OutputPart
	Checks      map[string]Check
	Unverified  []Unverified
	Reopened    bool
}

type item struct {
	Kind        string   `json:"kind"`
	Part        string   `json:"part"`
	ContentType string   `json:"content_type"`
	Source      string   `json:"source"`
	References  []string `json:"references"`
	Op          string   `json:"op"`
	Executed    bool     `json:"executed"`
	Residual    bool     `json:"residual"`
	Unit        string   `json:"unit"`
	Scope       string   `json:"scope"`
	Reason      string   `json:"reason"`
	InputParts  int      `json:"input_parts"`
	OutputParts int      `json:"output_parts"`
	Operations  int      `json:"operations"`
	Reopened    bool     `json:"reopened"`
}

// Runner calls format.validate.
type Runner struct{ Schemas *host.Schemas }

func NewRunner() (*Runner, error) {
	s, err := host.NewSchemas()
	if err != nil {
		return nil, err
	}
	if err = s.Compile("format.schema.json", "ValidateItem"); err != nil {
		return nil, err
	}
	return &Runner{Schemas: s}, nil
}

// Run is strict in the way export.Writer is: one completion marker, last, whose
// counts equal what arrived; each part once; each planned operation checked
// exactly once (unless the candidate could not be reopened, in which case none
// is); and a stream that ended completed. A plugin that cannot show all of that
// has not validated, whatever it claims.
func (r *Runner) Run(ctx context.Context, s *host.Session, a kernel.AttemptRef, input, plan, candidate refs.Wire, opIDs []string, look plan.Look) (Report, error) {
	if s == nil || input.Ref == "" || plan.Ref == "" || candidate.Ref == "" {
		return Report{}, ErrInvalid
	}
	planned := map[string]bool{}
	for _, id := range opIDs {
		planned[id] = true
	}
	rep := Report{Checks: map[string]Check{}}
	inSeen, outSeen := map[string]bool{}, map[string]bool{}
	var done *item
	out, err := s.Stream(ctx, "format.validate", a, look.AddTo(map[string]any{"input": input, "plan": plan, "candidate": candidate}),
		func(_ context.Context, body json.RawMessage) error {
			if done != nil || r.Schemas.Validate("format.schema.json", "ValidateItem", body) != nil {
				return ErrProtocol
			}
			var it item
			if json.Unmarshal(body, &it) != nil {
				return ErrProtocol
			}
			switch it.Kind {
			case "input_part":
				if inSeen[it.Part] {
					return ErrProtocol
				}
				inSeen[it.Part] = true
				rep.InputParts = append(rep.InputParts, coverage.InputPart{ID: it.Part, ContentType: it.ContentType})
			case "output_part":
				if outSeen[it.Part] {
					return ErrProtocol
				}
				outSeen[it.Part] = true
				rep.OutputParts = append(rep.OutputParts, coverage.OutputPart{ID: it.Part, ContentType: it.ContentType, SourceID: it.Source, References: it.References})
			case "op_check":
				if !planned[it.Op] {
					return ErrProtocol
				}
				if _, dup := rep.Checks[it.Op]; dup {
					return ErrProtocol
				}
				rep.Checks[it.Op] = Check{Executed: it.Executed, Residual: it.Residual, Unit: it.Unit}
			case "unverified":
				rep.Unverified = append(rep.Unverified, Unverified{Scope: it.Scope, Reason: it.Reason})
			case "validate_complete":
				done = &it
			default:
				return ErrProtocol
			}
			return nil
		})
	if err != nil {
		return Report{}, err
	}
	if out.Status != "completed" || done == nil || done.InputParts != len(rep.InputParts) || done.OutputParts != len(rep.OutputParts) || done.Operations != len(rep.Checks) {
		return Report{}, ErrProtocol
	}
	rep.Reopened = done.Reopened
	switch {
	case rep.Reopened && len(rep.Checks) != len(opIDs):
		return Report{}, ErrProtocol // every approved operation must be accounted for
	case !rep.Reopened && (len(rep.Checks) != 0 || len(rep.OutputParts) != 0):
		return Report{}, ErrProtocol // nothing can be claimed about a candidate that was not opened
	}
	SortUnverified(rep.Unverified)
	return rep, nil
}

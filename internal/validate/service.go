package validate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/extract"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/snapshot"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrStore = errors.New("VALIDATE_STORE_FAILED")
	ErrStale = errors.New("VALIDATE_STALE")
)

// Module owns the validation result: the latest conclusion for a job's candidate.
// Rows hold counts, codes and part names, never content.
func Module() store.Module {
	return store.Module{Name: "validate", Prefix: "validate_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE validate_result(job TEXT PRIMARY KEY, validation TEXT NOT NULL, plan_version TEXT NOT NULL, candidate TEXT NOT NULL,
			conclusion TEXT NOT NULL, status TEXT NOT NULL, new_findings INTEGER NOT NULL, generated INTEGER NOT NULL, unlocated INTEGER NOT NULL,
			residuals INTEGER NOT NULL, mismatches INTEGER NOT NULL, unaccounted INTEGER NOT NULL, kept_gaps INTEGER NOT NULL, unverified TEXT NOT NULL)`,
	}}}}
}

// Staged is what a validation run needs from the worker's side: read-only
// references to the input, the very plan the candidate was written from, and the
// candidate. Files are the worker's ExtraFiles in order.
type Staged struct {
	Files             []*os.File
	Input, Plan, Cand refs.Wire
	// Look is the one the candidate was written with: the regions must hold its texture.
	Look plan.Look
}

// Stage issues the three read grants of module design 8.2 step 1.
func Stage(m *refs.Manager, a kernel.AttemptRef, inputID, planResource, candidateResource string) (*Staged, error) {
	if m == nil || inputID == "" || planResource == "" || candidateResource == "" {
		return nil, ErrInvalid
	}
	var gs []*refs.Grant
	for _, id := range []string{inputID, planResource, candidateResource} {
		g, err := m.IssueRead(a, id)
		if err != nil {
			m.Revoke(a)
			return nil, err
		}
		gs = append(gs, g)
	}
	files, wires, err := refs.Handoff(gs...)
	if err != nil {
		m.Revoke(a)
		return nil, err
	}
	return &Staged{Files: files, Input: wires[0], Plan: wires[1], Cand: wires[2]}, nil
}

// Draft is an exception found after export. Type is an exceptions type code;
// validate may not import exceptions, so the wiring converts.
type Draft struct{ Type, ScopeKey, Reason string }

// Result is the recorded outcome of one validation.
type Result struct {
	Job, Validation, PlanVersion, Candidate string
	Conclusion                              Conclusion
	Status                                  Status
	NewFindings, Generated, Unlocated       int
	Residuals, Mismatches, Unaccounted      int
	KeptGaps                                bool
	Unverified                              []Unverified
	// ResidualUnits are what plan.Widen needs; they are not stored.
	ResidualUnits []plan.Residual
	ResultVersion uint64
}

// Service runs the validation stage.
type Service struct {
	Commit   func(ctx context.Context, fn func(store.Tx) error) error
	Admit    func(tx store.Tx, a kernel.AttemptRef) error
	Runner   *Runner
	Scanner  *extract.Pipeline
	Findings *findings.Repository
	Refs     *refs.Manager
	// UnitOf maps a candidate hit's input position (part, ordinal) to the text unit
	// the input run stored, with its text version.
	UnitOf func(tx store.Tx, part string, ordinal int) (unit string, version uint64, ok bool)
	// Value returns the original text of an input range, only to compute the
	// same-value HMAC; it is cleared by findings.Add. May be nil.
	Value func(tx store.Tx, unit string, start, end int) []byte
	// Persist stores the exceptions found after export in the same commit as the
	// findings (exceptions.Repository.SyncPostExport). Called with no drafts after a
	// clean validation so earlier ones are resolved.
	Persist func(tx store.Tx, job string, drafts []Draft, validation string) error
}

// Request is one validation of a registered candidate.
type Request struct {
	Session         *host.Session
	Attempt         kernel.AttemptRef
	Batch           string
	Stage           *Staged
	Plan            plan.Plan
	CandidateDigest string
	Destinations    []coverage.Destination
	Edits           []Edit
	Known           []Known
	WidenUsed       bool
	Widen           func([]plan.Residual) error
	Facet           snapshot.DetectFacet
	ExpectNoText    bool
	KeptGaps        bool
}

func (s *Service) ok() bool {
	return s != nil && s.Commit != nil && s.Admit != nil && s.Runner != nil && s.Scanner != nil && s.Findings != nil && s.UnitOf != nil && s.Persist != nil
}

// Run validates a candidate and records the outcome (module design 8.2). A run
// that could not finish returns NotRun with its error and records nothing, so the
// caller reruns it on the same candidate. Everything a finished run writes (new
// findings, exceptions found after export, the result) is one commit, checked for
// ownership.
func (s *Service) Run(ctx context.Context, r Request) (Result, error) {
	if !s.ok() || r.Session == nil || r.Stage == nil || r.Batch == "" || r.CandidateDigest == "" || r.Plan.Version == "" {
		return Result{}, ErrInvalid
	}
	a := r.Attempt
	ids := make([]string, len(r.Plan.Operations))
	for i, o := range r.Plan.Operations {
		ids[i] = o.ID
	}
	notRun := Result{Job: a.JobID, Conclusion: NotRun, Status: StatusNotRun}
	report, err := s.Runner.Run(ctx, r.Session, a, r.Stage.Input, r.Stage.Plan, r.Stage.Cand, ids, r.Stage.Look)
	if err != nil {
		return notRun, err
	}
	var scan extract.ScanResult
	if report.Reopened {
		scan, err = s.Scanner.Rescan(ctx, extract.Request{Session: r.Session, Attempt: a, Batch: r.Batch, Method: "format.extract",
			Params: map[string]any{"input": r.Stage.Cand}, Facet: r.Facet})
		if err != nil {
			return notRun, err
		}
	}
	out, err := Evaluate(Inputs{Plan: r.Plan, Report: report, Destinations: r.Destinations, Scan: scan, Edits: r.Edits, Known: r.Known,
		WidenUsed: r.WidenUsed, Widen: r.Widen, ExpectNoText: r.ExpectNoText})
	if err != nil {
		return notRun, err
	}
	validation := fmt.Sprintf("%s:%.16s", r.Plan.Version, r.CandidateDigest)
	res := Result{Job: a.JobID, Validation: validation, PlanVersion: r.Plan.Version, Candidate: r.CandidateDigest, Conclusion: out.Conclusion,
		Status: StatusOf(out.Conclusion, r.KeptGaps), NewFindings: len(out.Association.New), Generated: len(out.Association.Generated),
		Unlocated: len(out.Association.Unlocated), Residuals: len(out.Residuals), Mismatches: out.Facts.Mismatches, Unaccounted: len(out.Unaccounted),
		KeptGaps: r.KeptGaps, Unverified: out.Unverified, ResidualUnits: dedupe(out.Residuals)}
	soundOutput := out.Conclusion == Passed || out.Conclusion == NewFindings

	err = s.Commit(ctx, func(tx store.Tx) error {
		if err := s.Admit(tx, a); err != nil {
			return ErrStale
		}
		if soundOutput {
			items, drafts := s.newFindings(tx, out.Association)
			// a hit that cannot be tied to a stored unit is handled like an unlocated one
			res.NewFindings, res.Unlocated = len(items), len(drafts)-len(out.Association.Generated)
			if len(items) > 0 {
				if _, err := s.Findings.Add(tx, a, r.Batch, findings.StagePostExport, items); err != nil {
					return err
				}
				v, err := s.Findings.Seal(tx, a, r.Batch, findings.StagePostExport)
				if err != nil {
					return err
				}
				res.ResultVersion = v
			}
			if err := s.Persist(tx, a.JobID, drafts, validation); err != nil {
				return err
			}
		}
		return save(tx, res)
	})
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, ErrStale):
		return notRun, err
	}
	return notRun, ErrStore
}

// newFindings turns located new hits into post-export findings; generated-content
// hits, unlocated hits and new hits whose unit the input run did not store become
// exception drafts (module design 8.2 step 4). Drafts come out sorted by the
// association order, generated first.
func (s *Service) newFindings(tx store.Tx, as Association) ([]findings.New, []Draft) {
	var items []findings.New
	var drafts []Draft
	for _, g := range as.Generated {
		drafts = append(drafts, Draft{"policy_review", fmt.Sprintf("gen:%s#%d:%d-%d", g.Part, g.Ordinal, g.Start, g.End), "generated_content_hit"})
	}
	for _, h := range as.Unlocated {
		drafts = append(drafts, Draft{"unreliable_location", fmt.Sprintf("cand:%s#%d:%d-%d", h.Part, h.Ordinal, h.Start, h.End), "candidate_hit_unlocated"})
	}
	for _, n := range as.New {
		unit, ver, ok := s.UnitOf(tx, n.Part, n.Ordinal)
		if !ok {
			drafts = append(drafts, Draft{"unreliable_location", fmt.Sprintf("in:%s#%d:%d-%d", n.Part, n.Ordinal, n.Start, n.End), "new_hit_unit_unknown"})
			continue
		}
		it := findings.New{Unit: unit, TextVersion: ver, Start: n.Start, End: n.End, Category: n.Category, Source: n.Source, Score: n.Score,
			Mismatch: n.Mismatch, Template: n.Part}
		if s.Value != nil {
			it.Value = s.Value(tx, unit, n.Start, n.End)
		}
		items = append(items, it)
	}
	return items, drafts
}

func save(tx store.Tx, r Result) error {
	t, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	sc, err := t.Module("validate")
	if err != nil {
		return err
	}
	un, err := json.Marshal(r.Unverified)
	if err != nil {
		return err
	}
	_, err = sc.Exec(`INSERT INTO validate_result(job, validation, plan_version, candidate, conclusion, status, new_findings, generated, unlocated, residuals, mismatches, unaccounted, kept_gaps, unverified)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job) DO UPDATE SET validation=excluded.validation, plan_version=excluded.plan_version, candidate=excluded.candidate,
		conclusion=excluded.conclusion, status=excluded.status, new_findings=excluded.new_findings, generated=excluded.generated, unlocated=excluded.unlocated,
		residuals=excluded.residuals, mismatches=excluded.mismatches, unaccounted=excluded.unaccounted, kept_gaps=excluded.kept_gaps, unverified=excluded.unverified`,
		r.Job, r.Validation, r.PlanVersion, r.Candidate, string(r.Conclusion), string(r.Status), r.NewFindings, r.Generated, r.Unlocated,
		r.Residuals, r.Mismatches, r.Unaccounted, b2i(r.KeptGaps), string(un))
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Latest returns the job's recorded validation; false if there is none.
func Latest(tx store.Tx, job string) (Result, bool, error) {
	if job == "" {
		return Result{}, false, ErrInvalid
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return Result{}, false, err
	}
	r := Result{Job: job}
	var conclusion, status, un string
	var kept int
	switch err = t.QueryRow(`SELECT validation, plan_version, candidate, conclusion, status, new_findings, generated, unlocated, residuals, mismatches, unaccounted, kept_gaps, unverified
		FROM validate_result WHERE job=?`, job).Scan(&r.Validation, &r.PlanVersion, &r.Candidate, &conclusion, &status, &r.NewFindings, &r.Generated, &r.Unlocated,
		&r.Residuals, &r.Mismatches, &r.Unaccounted, &kept, &un); {
	case errors.Is(err, sql.ErrNoRows):
		return Result{}, false, nil
	case err != nil:
		return Result{}, false, err
	}
	r.Conclusion, r.Status, r.KeptGaps = Conclusion(conclusion), Status(status), kept != 0
	if err = json.Unmarshal([]byte(un), &r.Unverified); err != nil {
		return Result{}, false, err
	}
	return r, true, nil
}

// Purge deletes a job's validation record (retention).
func Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	sc, err := t.Module("validate")
	if err != nil {
		return err
	}
	_, err = sc.Exec("DELETE FROM validate_result WHERE job=?", job)
	return err
}

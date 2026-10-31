package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrInvalid            = errors.New("POLICY_INVALID")
	ErrInputsChanged      = errors.New("POLICY_INPUTS_CHANGED")
	ErrCoverageIncomplete = errors.New("POLICY_COVERAGE_INCOMPLETE")
	ErrStore              = errors.New("POLICY_STORE_FAILED")
)

// Module owns the stored resolution: the key and the dispositions plan reads.
// Rows hold ids, positions and actions, never text.
func Module() store.Module {
	return store.Module{Name: "policy", Prefix: "policy_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE policy_resolution(job TEXT PRIMARY KEY, key TEXT NOT NULL, detect_version INTEGER NOT NULL, decision_version INTEGER NOT NULL,
			dispose_digest TEXT NOT NULL, stats TEXT NOT NULL, dispositions INTEGER NOT NULL, drafts INTEGER NOT NULL)`,
		`CREATE TABLE policy_disposition(job TEXT NOT NULL, seq INTEGER NOT NULL, id TEXT NOT NULL, unit TEXT NOT NULL, start INTEGER NOT NULL,
			stop INTEGER NOT NULL, category TEXT NOT NULL, action TEXT NOT NULL, basis TEXT NOT NULL, merged TEXT NOT NULL, PRIMARY KEY(job, seq))`,
	}}}}
}

// Commit is workspace.Commit.
type Commit func(ctx context.Context, fn func(store.Tx) error) error

// Enrichment carries facts about a finding that live outside the findings
// table: the source map's reliability (INV-09), a keep exception of the policy,
// and a structured value whose type or key would change.
type Enrichment struct {
	KeepByPolicy    bool
	Unreliable      bool
	NeedsTypeChange bool
}

// Service is the resolve stage. Every field is required except Enrich and Retries.
type Service struct {
	Commit   Commit
	Findings *findings.Repository
	Retries  int // recomputations when decisions change underneath; 0 means 3
	Enrich   func(findings.Finding) Enrichment
}

func (s *Service) ok() bool { return s != nil && s.Commit != nil && s.Findings != nil }

// decisionVersion counts review decisions and exception decisions together. Both
// only rise, so their sum rises whenever either does.
func (s *Service) decisionVersion(tx store.Tx, job string, out *uint64) error {
	a, err := review.Version(tx, job)
	if err != nil {
		return err
	}
	b, err := (exceptions.Repository{}).Version(tx, job)
	if err != nil {
		return err
	}
	*out = a + b
	return nil
}

func (s *Service) retries() int {
	if s.Retries < 1 {
		return 3
	}
	return s.Retries
}

// Request is one job's resolution.
type Request struct {
	Job           string
	Policy        Policy
	DisposeDigest string // digest of the frozen dispose facet
}

// Result summarises a committed resolution.
type Result struct {
	Key             string
	DetectVersion   uint64
	DecisionVersion uint64
	Stats           Stats
	Dispositions    int
	Drafts          int
	Sync            exceptions.SyncResult
	Attempts        int
}

type snapshot struct {
	detect, decision uint64
	in               Input
}

// read gathers everything Resolve needs, page by page in short transactions,
// and remembers the versions it read against.
func (s *Service) read(ctx context.Context, job string, p Policy, units []string, withGaps bool) (snapshot, error) {
	var sn snapshot
	sn.in.Policy = p
	err := s.Commit(ctx, func(tx store.Tx) (err error) {
		if sn.detect, err = s.Findings.ResultVersion(tx, job); err != nil {
			return err
		}
		return s.decisionVersion(tx, job, &sn.decision)
	})
	if err != nil {
		return sn, ErrStore
	}
	var all []findings.Finding
	after := int64(0)
	for {
		var page []findings.Finding
		if err = s.Commit(ctx, func(tx store.Tx) (e error) {
			page, e = s.Findings.Page(tx, findings.Filter{Job: job, Units: units}, after, findings.MaxPage)
			return e
		}); err != nil {
			return sn, ErrStore
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		after = page[len(page)-1].ID
	}
	sn.in.Decisions = map[string]Decision{}
	var applied map[int64]review.Applied
	var exc map[string]exceptions.Decision
	var parts []coverage.Part
	var complete, noInventory bool
	err = s.Commit(ctx, func(tx store.Tx) (e error) {
		applied = map[int64]review.Applied{}
		for i := 0; i < len(all); i += findings.MaxPage {
			a, e := review.Effective(tx, all[i:min(i+findings.MaxPage, len(all))])
			if e != nil {
				return e
			}
			for k, v := range a {
				applied[k] = v
			}
		}
		if exc, e = (exceptions.Repository{}).Decisions(tx, job); e != nil {
			return e
		}
		if withGaps {
			parts, complete, e = coverage.Repository{}.Ledger(tx, job)
			if errors.Is(e, coverage.ErrNoInventory) {
				noInventory, e = true, nil
			}
		}
		return e
	})
	if err != nil {
		return sn, ErrStore
	}
	for _, f := range all {
		c := Candidate{ID: strconv.FormatInt(f.ID, 10), Unit: f.Unit, Start: f.Start, End: f.End, Category: f.Category,
			Source: f.Source, Score: f.Score, ValidatorMismatch: f.Mismatch}
		if s.Enrich != nil {
			e := s.Enrich(f)
			c.KeepByPolicy, c.Unreliable, c.NeedsTypeChange = e.KeepByPolicy, e.Unreliable, e.NeedsTypeChange
		}
		sn.in.Candidates = append(sn.in.Candidates, c)
		if a, ok := applied[f.ID]; ok {
			sn.in.Decisions[c.ID] = Decision{Keep: a.Keep, Bulk: a.Bulk}
			continue
		}
		// a decision taken on the exception of this candidate counts as a human
		// item decision on the candidate itself
		scope := scopeKey(c.Unit, c.Start, c.End)
		for _, t := range []exceptions.Type{exceptions.ValidatorMismatch, exceptions.TypeChange, exceptions.PolicyReview, exceptions.UnreliableLocation} {
			if d, ok := exc[exceptions.ItemIDFor(job, t, scope)]; ok {
				sn.in.Decisions[c.ID] = Decision{Keep: d.Action == exceptions.Keep}
				break
			}
		}
	}
	if withGaps {
		if noInventory {
			return sn, ErrCoverageIncomplete
		}
		rep, err := coverage.Assess(parts, complete)
		if err != nil {
			return sn, ErrCoverageIncomplete
		}
		sn.in.Gaps = append(append([]coverage.Gap(nil), rep.Unresolved...), rep.Retained...)
		sn.in.GapDecisions = map[string]Decision{}
		for _, g := range sn.in.Gaps {
			if d, ok := exc[exceptions.ItemIDFor(job, exceptions.Type(g.Kind), "part:"+g.PartID)]; ok {
				sn.in.GapDecisions[g.PartID] = Decision{Keep: d.Action == exceptions.Keep}
			}
		}
	}
	return sn, nil
}

// Run resolves a job and commits the result against the versions it read: if the
// detection result or the decisions moved meanwhile, nothing is stored and the
// computation is repeated, up to Retries times (module design 7.1).
func (s *Service) Run(ctx context.Context, r Request) (Result, error) {
	if !s.ok() || r.Job == "" || r.DisposeDigest == "" {
		return Result{}, ErrInvalid
	}
	for attempt := 1; attempt <= s.retries(); attempt++ {
		sn, err := s.read(ctx, r.Job, r.Policy, nil, true)
		if err != nil {
			return Result{}, err
		}
		out := Resolve(sn.in)
		key := ResolutionKey(sn.detect, r.DisposeDigest, sn.decision)
		res := Result{Key: key, DetectVersion: sn.detect, DecisionVersion: sn.decision, Stats: out.Stats,
			Dispositions: len(out.Dispositions), Drafts: len(out.Drafts), Attempts: attempt}
		var moved bool
		err = s.Commit(ctx, func(tx store.Tx) error {
			d, err := s.Findings.ResultVersion(tx, r.Job)
			if err != nil {
				return err
			}
			var v uint64
			if err = s.decisionVersion(tx, r.Job, &v); err != nil {
				return err
			}
			if d != sn.detect || v != sn.decision {
				moved = true
				return nil // nothing written: compute again
			}
			drafts := make([]exceptions.Draft, len(out.Drafts))
			for i, x := range out.Drafts {
				drafts[i] = exceptions.Draft{Type: x.Type, ScopeKey: x.ScopeKey, Reason: x.Reason}
			}
			if res.Sync, err = (exceptions.Repository{}).Sync(tx, r.Job, drafts, key); err != nil {
				return err
			}
			return store1(tx, r, res, out)
		})
		switch {
		case err != nil:
			if errors.Is(err, ErrInvalid) {
				return Result{}, err
			}
			return Result{}, ErrStore
		case !moved:
			return res, nil
		}
	}
	return Result{}, ErrInputsChanged
}

func store1(tx store.Tx, r Request, res Result, out Output) error {
	t, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	sc, err := t.Module("policy")
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM policy_disposition WHERE job=?", "DELETE FROM policy_resolution WHERE job=?"} {
		if _, err = sc.Exec(q, r.Job); err != nil {
			return err
		}
	}
	stats, _ := json.Marshal(res.Stats)
	if _, err = sc.Exec(`INSERT INTO policy_resolution(job, key, detect_version, decision_version, dispose_digest, stats, dispositions, drafts) VALUES(?,?,?,?,?,?,?,?)`,
		r.Job, res.Key, res.DetectVersion, res.DecisionVersion, r.DisposeDigest, string(stats), res.Dispositions, res.Drafts); err != nil {
		return err
	}
	for i, d := range out.Dispositions {
		merged, _ := json.Marshal(d.Merged)
		if _, err = sc.Exec(`INSERT INTO policy_disposition(job, seq, id, unit, start, stop, category, action, basis, merged) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			r.Job, i+1, d.ID, d.Unit, d.Start, d.End, d.Category, string(d.Action), string(d.Basis), string(merged)); err != nil {
			return err
		}
	}
	tx.Emit(store.Event{Type: "ResolutionChanged", Subject: kernel.ArtifactRef{Kind: "resolution", ID: r.Job, Digest: res.Key}})
	return nil
}

// Stored is a committed resolution header.
type Stored struct {
	Key             string
	DetectVersion   uint64
	DecisionVersion uint64
	DisposeDigest   string
	Stats           Stats
	Dispositions    int
	Drafts          int
}

// Resolution reads the committed header; the second result is false if the job
// has not been resolved.
func Resolution(tx store.Tx, job string) (Stored, bool, error) {
	if job == "" {
		return Stored{}, false, ErrInvalid
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return Stored{}, false, err
	}
	var s Stored
	var stats string
	switch err = t.QueryRow(`SELECT key, detect_version, decision_version, dispose_digest, stats, dispositions, drafts FROM policy_resolution WHERE job=?`, job).
		Scan(&s.Key, &s.DetectVersion, &s.DecisionVersion, &s.DisposeDigest, &stats, &s.Dispositions, &s.Drafts); {
	case errors.Is(err, sql.ErrNoRows):
		return Stored{}, false, nil
	case err != nil:
		return Stored{}, false, err
	}
	if err = json.Unmarshal([]byte(stats), &s.Stats); err != nil {
		return Stored{}, false, err
	}
	return s, true, nil
}

// MaxPage bounds a page of dispositions.
const MaxPage = 500

// Dispositions pages the committed dispositions of a job in sequence order, for
// plan to read. The page is only meaningful for the key it was read under, which
// the caller checks with Resolution in the same transaction.
func Dispositions(tx store.Tx, job string, after, limit int) ([]Disposition, error) {
	if job == "" || after < 0 || limit < 1 || limit > MaxPage {
		return nil, ErrInvalid
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT id, unit, start, stop, category, action, basis, merged FROM policy_disposition WHERE job=? AND seq>? ORDER BY seq LIMIT ?`, job, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Disposition
	for rows.Next() {
		var d Disposition
		var action, basis, merged string
		if err = rows.Scan(&d.ID, &d.Unit, &d.Start, &d.End, &d.Category, &action, &basis, &merged); err != nil {
			return nil, err
		}
		d.Action, d.Basis = Action(action), Basis(basis)
		if err = json.Unmarshal([]byte(merged), &d.Merged); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Purge deletes a job's stored resolution (retention).
func Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	sc, err := t.Module("policy")
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM policy_disposition WHERE job=?", "DELETE FROM policy_resolution WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

// MaxViewport bounds the units a simulation covers.
const MaxViewport = 200

// Simulate resolves only the targets in the given units (the viewport) and
// returns the result without writing anything: no resolution, no exception, no
// decision changes (module design 7.1). Draft decisions that are not yet
// committed may be supplied; they override committed ones for this call only.
// Coverage gaps are outside a viewport and are not part of the result.
func (s *Service) Simulate(ctx context.Context, job string, p Policy, units []string, draft map[int64]Decision) (Output, error) {
	if !s.ok() || job == "" || len(units) == 0 || len(units) > MaxViewport {
		return Output{}, ErrInvalid
	}
	sn, err := s.read(ctx, job, p, units, false)
	if err != nil {
		return Output{}, err
	}
	for id, d := range draft {
		sn.in.Decisions[strconv.FormatInt(id, 10)] = d
	}
	return Resolve(sn.in), nil
}

func scopeKey(unit string, start, end int) string { return fmt.Sprintf("%s:%d-%d", unit, start, end) }

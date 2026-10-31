package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/plan"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

var (
	ErrInvalid     = errors.New("EXPORT_INVALID")
	ErrProtocol    = errors.New("EXPORT_PROTOCOL_VIOLATION")
	ErrNotExecuted = errors.New("EXPORT_OPERATION_NOT_EXECUTED")
	ErrStore       = errors.New("EXPORT_STORE_FAILED")
	ErrEmpty       = errors.New("EXPORT_CANDIDATE_EMPTY")
)

// MaxPlanBytes bounds the plan resource the host writes.
var MaxPlanBytes = 64 << 20

// Staged is what a format.write call needs: three references and the files that
// carry them to the worker. The caller passes Files as the worker's ExtraFiles,
// in order, and must call Release when the stage is over.
type Staged struct {
	Files     []*os.File
	Input     refs.Wire
	Plan      refs.Wire
	Candidate refs.Wire
	// PlanResource is the frozen plan's resource id, kept so validation reads the
	// very plan the candidate was written from.
	PlanResource string
	// Look is passed to format.write with the plan.
	Look      plan.Look
	candidate *refs.Grant
	grants    []*refs.Grant
}

// Stage writes the plan as an encrypted, frozen resource of the job and issues
// the three grants of module design 8.1: input read-only, plan read-only and a
// fresh append-only candidate. replacement supplies each text operation's
// replacement text. The plan is a host-written resource (refs.HostWriter), so no
// plugin ever sees another operation list than the one the kernel approved.
func Stage(m *refs.Manager, a kernel.AttemptRef, inputID string, p plan.Plan, replacement func(plan.Operation) (string, error), candidateLimit int64) (*Staged, error) {
	if m == nil || inputID == "" || candidateLimit <= 0 || replacement == nil {
		return nil, ErrInvalid
	}
	data, err := plan.Encode(p, replacement)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxPlanBytes {
		return nil, ErrInvalid
	}
	pg, err := m.IssueWrite(a, int64(len(data))+1<<20)
	if err != nil {
		return nil, err
	}
	w, err := pg.HostWriter(uint64(len(data)) + 1<<20)
	if err == nil {
		_, err = w.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err == nil {
		_, err = pg.Freeze()
	}
	clear(data)
	if err != nil {
		m.Revoke(a)
		return nil, ErrStore
	}
	in, err := m.IssueRead(a, inputID)
	if err != nil {
		m.Revoke(a)
		return nil, err
	}
	pr, err := m.IssueRead(a, pg.ResourceID())
	if err != nil {
		m.Revoke(a)
		return nil, err
	}
	cand, err := m.IssueWrite(a, candidateLimit)
	if err != nil {
		m.Revoke(a)
		return nil, err
	}
	files, wires, err := refs.Handoff(in, pr, cand)
	if err != nil {
		m.Revoke(a)
		return nil, err
	}
	return &Staged{Files: files, Input: wires[0], Plan: wires[1], Candidate: wires[2], PlanResource: pg.ResourceID(), candidate: cand, grants: []*refs.Grant{in, pr, cand}}, nil
}

// Result is what a completed write reported, already checked against the plan.
type Result struct {
	Executed     int
	Destinations []coverage.Destination
	Bytes        int64
}

// NotExecuted lists operations the plugin could not perform, by id and reason
// code; the file's export fails if there is any (module design 8.1 step 4).
type NotExecuted struct {
	Op     string
	Reason string
}

type writeItem struct {
	Kind         string `json:"kind"`
	Op           string `json:"op"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	Part         string `json:"part"`
	Fate         string `json:"fate"`
	ExpectedType string `json:"expected_type"`
	Operations   int    `json:"operations"`
	Parts        int    `json:"parts"`
	Bytes        int64  `json:"bytes"`
}

// Writer runs format.write. Schemas must have WriteItem compiled.
type Writer struct{ Schemas *host.Schemas }

func NewWriter() (*Writer, error) {
	s, err := host.NewSchemas()
	if err != nil {
		return nil, err
	}
	if err = s.Compile("format.schema.json", "WriteItem"); err != nil {
		return nil, err
	}
	return &Writer{Schemas: s}, nil
}

// Write calls format.write and accounts for every approved operation. It is
// strict about the stream: one op_result per planned operation and no others,
// at most one fate per part, exactly one write_complete as the last item whose
// counts equal what was received, and a stream that ended completed. Anything
// else is a protocol violation, and so is a stream that ended failed; the
// plugin's own verdict is advisory.
func (w *Writer) Write(ctx context.Context, s *host.Session, a kernel.AttemptRef, st *Staged, p plan.Plan) (Result, []NotExecuted, error) {
	if s == nil || st == nil {
		return Result{}, nil, ErrInvalid
	}
	planned := map[string]bool{}
	for _, o := range p.Operations {
		planned[o.ID] = true
	}
	results := map[string]string{}
	var failed []NotExecuted
	fates := map[string]coverage.Destination{}
	var order []string
	done := false
	var complete writeItem
	out, err := s.Stream(ctx, "format.write", a, st.Look.AddTo(map[string]any{"input": st.Input, "plan": st.Plan, "candidate": st.Candidate}),
		func(_ context.Context, body json.RawMessage) error {
			if done {
				return ErrProtocol // nothing may follow the completion marker
			}
			if err := w.Schemas.Validate("format.schema.json", "WriteItem", body); err != nil {
				return ErrProtocol
			}
			var it writeItem
			if json.Unmarshal(body, &it) != nil {
				return ErrProtocol
			}
			switch it.Kind {
			case "op_result":
				if !planned[it.Op] {
					return ErrProtocol
				}
				if _, dup := results[it.Op]; dup {
					return ErrProtocol
				}
				if it.Status == "executed" && it.Reason != "" {
					return ErrProtocol
				}
				results[it.Op] = it.Status
				if it.Status != "executed" {
					failed = append(failed, NotExecuted{Op: it.Op, Reason: it.Reason})
				}
			case "part_fate":
				if _, dup := fates[it.Part]; dup {
					return ErrProtocol
				}
				if (it.Fate == "converted") != (it.ExpectedType != "") {
					return ErrProtocol
				}
				fates[it.Part] = coverage.Destination{PartID: it.Part, Fate: coverage.Fate(it.Fate), ExpectedType: it.ExpectedType}
				order = append(order, it.Part)
			case "write_complete":
				done, complete = true, it
			default:
				return ErrProtocol
			}
			return nil
		})
	if err != nil {
		return Result{}, nil, err
	}
	if out.Status != "completed" || !done || complete.Operations != len(p.Operations) || complete.Parts != len(fates) || len(results) != len(p.Operations) {
		return Result{}, nil, ErrProtocol
	}
	res := Result{Bytes: complete.Bytes}
	for _, id := range order {
		res.Destinations = append(res.Destinations, fates[id])
	}
	for _, st := range results {
		if st == "executed" {
			res.Executed++
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Op < failed[j].Op })
	if len(failed) > 0 {
		return res, failed, fmt.Errorf("%w: %d of %d", ErrNotExecuted, len(failed), len(p.Operations))
	}
	return res, nil, nil
}

// Release revokes the stage's grants. An unfrozen candidate is discarded.
func (st *Staged) Release(m *refs.Manager, a kernel.AttemptRef) { m.Revoke(a) }

// Freeze seals the candidate and returns its resource id and digest. An empty
// candidate is refused.
func (st *Staged) Freeze(bytes int64) (id, digest string, err error) {
	if bytes <= 0 {
		return "", "", ErrEmpty
	}
	digest, err = st.candidate.Freeze()
	if err != nil {
		return "", "", err
	}
	return st.candidate.ResourceID(), digest, nil
}

// Module owns the candidate registration: which candidate belongs to which plan
// version and what the plugin claimed for each part. No content.
func Module() store.Module {
	return store.Module{Name: "export", Prefix: "export_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE export_candidate(job TEXT PRIMARY KEY, plan_version TEXT NOT NULL, plan_resource TEXT NOT NULL, resource TEXT NOT NULL, digest TEXT NOT NULL,
			bytes INTEGER NOT NULL, operations INTEGER NOT NULL)`,
		`CREATE TABLE export_destination(job TEXT NOT NULL, part TEXT NOT NULL, fate TEXT NOT NULL, expected_type TEXT NOT NULL, PRIMARY KEY(job, part))`,
	}}}}
}

// Candidate is a registered candidate.
type Candidate struct {
	Job          string
	PlanVersion  string
	PlanResource string
	Resource     string
	Digest       string
	Bytes        int64
	Operations   int
}

type Repository struct{}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := t.Module("export")
	return t, sc, err
}

// Register records the candidate for the job and returns the resource of the one
// it replaces, for deletion. The caller has checked admission in the same
// transaction.
func (Repository) Register(tx store.Tx, c Candidate, dests []coverage.Destination) (previous string, err error) {
	if c.Job == "" || c.PlanVersion == "" || c.PlanResource == "" || c.Resource == "" || c.Digest == "" || c.Bytes <= 0 || len(dests) == 0 {
		return "", ErrInvalid
	}
	t, sc, err := scope(tx)
	if err != nil {
		return "", err
	}
	_ = t.QueryRow("SELECT resource FROM export_candidate WHERE job=?", c.Job).Scan(&previous)
	if _, err = sc.Exec("DELETE FROM export_destination WHERE job=?", c.Job); err != nil {
		return "", err
	}
	if _, err = sc.Exec(`INSERT INTO export_candidate(job, plan_version, plan_resource, resource, digest, bytes, operations) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(job) DO UPDATE SET plan_version=excluded.plan_version, plan_resource=excluded.plan_resource, resource=excluded.resource, digest=excluded.digest, bytes=excluded.bytes, operations=excluded.operations`,
		c.Job, c.PlanVersion, c.PlanResource, c.Resource, c.Digest, c.Bytes, c.Operations); err != nil {
		return "", err
	}
	for _, d := range dests {
		if _, err = sc.Exec("INSERT INTO export_destination(job, part, fate, expected_type) VALUES(?,?,?,?)", c.Job, d.PartID, string(d.Fate), d.ExpectedType); err != nil {
			return "", err
		}
	}
	tx.Emit(store.Event{Type: "CandidateRegistered", Subject: kernel.ArtifactRef{Kind: "candidate", ID: c.Job, Digest: c.Digest}})
	return previous, nil
}

// Current returns the job's registered candidate and its destinations.
func (Repository) Current(tx store.Tx, job string) (Candidate, []coverage.Destination, bool, error) {
	if job == "" {
		return Candidate{}, nil, false, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return Candidate{}, nil, false, err
	}
	c := Candidate{Job: job}
	if err = t.QueryRow("SELECT plan_version, plan_resource, resource, digest, bytes, operations FROM export_candidate WHERE job=?", job).
		Scan(&c.PlanVersion, &c.PlanResource, &c.Resource, &c.Digest, &c.Bytes, &c.Operations); err != nil {
		return Candidate{}, nil, false, nil
	}
	rows, err := t.Query("SELECT part, fate, expected_type FROM export_destination WHERE job=? ORDER BY part", job)
	if err != nil {
		return Candidate{}, nil, false, err
	}
	defer rows.Close()
	var ds []coverage.Destination
	for rows.Next() {
		var d coverage.Destination
		var fate string
		if err = rows.Scan(&d.PartID, &fate, &d.ExpectedType); err != nil {
			return Candidate{}, nil, false, err
		}
		d.Fate = coverage.Fate(fate)
		ds = append(ds, d)
	}
	return c, ds, true, rows.Err()
}

// Purge deletes a job's registration (retention). The candidate resource itself
// is removed with the job's resources by refs.
func (Repository) Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM export_destination WHERE job=?", "DELETE FROM export_candidate WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

// Stage freezes the candidate and registers it, with admission checked in the
// same commit. The stage run ends here: a failure leaves no registration.
type Service struct {
	WS   *workspace.Workspace
	Refs *refs.Manager
	Repo Repository
	W    *Writer
}

// Export runs the whole stage on a started worker. The caller has launched the
// worker with st.Files and initialised its session.
func (s *Service) Export(ctx context.Context, sess *host.Session, a kernel.AttemptRef, st *Staged, p plan.Plan) (Candidate, []NotExecuted, error) {
	if s == nil || s.WS == nil || s.Refs == nil || s.W == nil || p.Version == "" {
		return Candidate{}, nil, ErrInvalid
	}
	res, failed, err := s.W.Write(ctx, sess, a, st, p)
	if err != nil {
		st.Release(s.Refs, a)
		return Candidate{}, failed, err
	}
	id, digest, err := st.Freeze(res.Bytes)
	if err != nil {
		st.Release(s.Refs, a)
		return Candidate{}, nil, err
	}
	c := Candidate{Job: a.JobID, PlanVersion: p.Version, PlanResource: st.PlanResource, Resource: id, Digest: digest, Bytes: res.Bytes, Operations: len(p.Operations)}
	if err = s.WS.Commit(ctx, func(tx store.Tx) error {
		if err := s.WS.AdmitTx(tx, a); err != nil {
			return err
		}
		_, err := s.Repo.Register(tx, c, res.Destinations)
		return err
	}); err != nil {
		st.Release(s.Refs, a)
		if errors.Is(err, workspace.ErrStale) {
			return Candidate{}, nil, err
		}
		return Candidate{}, nil, ErrStore
	}
	return c, nil, nil
}

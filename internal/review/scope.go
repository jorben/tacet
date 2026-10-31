package review

import (
	"context"
	"errors"
	"sort"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/store"
)

// ScopeKind is how far a batch decision reaches (module design 7.3).
type ScopeKind string

const (
	ScopePage     ScopeKind = "page"     // exactly the findings on the page the user sees
	ScopeSelected ScopeKind = "selected" // exactly the findings the user ticked
	ScopeFilter   ScopeKind = "filter"   // everything the current filter matches
)

// Ref names one finding of one job.
type Ref struct {
	Job     string
	Finding int64
}

// ScopeSpec is the reach of a batch decision. Page and Selected carry explicit
// references; Filter carries the query, which Count and Materialize both run.
type ScopeSpec struct {
	Kind   ScopeKind
	Refs   []Ref
	Filter findings.Filter
}

// MaxExplicit bounds an explicit scope; anything larger is a filter scope.
const MaxExplicit = 5000

func (sp ScopeSpec) valid() bool {
	switch sp.Kind {
	case ScopePage, ScopeSelected:
		for _, r := range sp.Refs {
			if r.Job == "" || r.Finding < 1 {
				return false
			}
		}
		return len(sp.Refs) > 0 && len(sp.Refs) <= MaxExplicit
	case ScopeFilter:
		return sp.Filter.Job != "" || sp.Filter.Batch != ""
	}
	return false
}

// Count is the number of members the scope would have now.
func (s *Service) Count(ctx context.Context, sp ScopeSpec) (int, error) {
	if !s.ok() || !sp.valid() {
		return 0, ErrInvalid
	}
	n := 0
	err := s.walk(ctx, sp, func(chunk []findings.Finding) error { n += len(chunk); return nil })
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

func wrap(err error) error {
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrScopeChanged) || errors.Is(err, ErrNotFound) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrStore
}

// walk reads the scope in chunks, each in its own short transaction, so a large
// scope never holds the commit section.
func (s *Service) walk(ctx context.Context, sp ScopeSpec, fn func([]findings.Finding) error) error {
	if sp.Kind == ScopeFilter {
		after := int64(0)
		for {
			var page []findings.Finding
			if err := s.Commit(ctx, func(tx store.Tx) (err error) {
				page, err = s.Findings.Page(tx, sp.Filter, after, s.chunk())
				return err
			}); err != nil {
				return err
			}
			if len(page) == 0 {
				return nil
			}
			if err := fn(page); err != nil {
				return err
			}
			after = page[len(page)-1].ID
		}
	}
	byJob := map[string][]int64{}
	seen := map[Ref]bool{}
	for _, r := range sp.Refs {
		if r.Job == "" || r.Finding < 1 {
			return ErrInvalid
		}
		if !seen[r] {
			seen[r] = true
			byJob[r.Job] = append(byJob[r.Job], r.Finding)
		}
	}
	jobs := make([]string, 0, len(byJob))
	for j := range byJob {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	for _, job := range jobs {
		ids := byJob[job]
		for len(ids) > 0 {
			n := min(len(ids), s.chunk())
			var got []findings.Finding
			if err := s.Commit(ctx, func(tx store.Tx) (err error) {
				got, err = s.Findings.ByIDs(tx, job, ids[:n])
				return err
			}); err != nil {
				return err
			}
			if len(got) != n {
				return ErrNotFound // a ticked finding that is not (or no longer) visible
			}
			if err := fn(got); err != nil {
				return err
			}
			ids = ids[n:]
		}
	}
	return nil
}

// Impact says what a batch decision would replace.
type Impact struct {
	Members       int
	ItemDecisions int // members that already carry an effective single decision
}

// ImpactOf shows, before a batch decision is made, how many of its members the
// user has already decided one by one. By default the batch does not override
// them (BUX-08); only an explicit Replace does.
func (s *Service) ImpactOf(ctx context.Context, sp ScopeSpec) (Impact, error) {
	if !s.ok() || !sp.valid() {
		return Impact{}, ErrInvalid
	}
	var out Impact
	err := s.walk(ctx, sp, func(chunk []findings.Finding) error {
		out.Members += len(chunk)
		return s.Commit(ctx, func(tx store.Tx) error {
			eff, err := Effective(tx, chunk)
			if err != nil {
				return err
			}
			for _, f := range chunk {
				if e, ok := eff[f.ID]; ok && !e.Bulk {
					out.ItemDecisions++
				}
			}
			return nil
		})
	})
	if err != nil {
		return Impact{}, wrap(err)
	}
	return out, nil
}

// BulkRequest is one batch decision.
type BulkRequest struct {
	Request string
	Batch   string
	Scope   ScopeSpec
	Keep    bool
	// Replace also supersedes single decisions inside the scope. Without it they
	// keep priority and the bulk decision only fills the rest.
	Replace bool
}

// DecideBulk commits a batch decision in two phases (MD-11): a pending record,
// then members in chunks outside the commit section, then one commit that checks
// that no member's job changed its detection result and flips the record to
// committed. A crash leaves only a pending record, which nothing reads.
func (s *Service) DecideBulk(ctx context.Context, r BulkRequest) (string, int, error) {
	if !s.ok() || !validRequest(r.Request) || r.Batch == "" || !r.Scope.valid() {
		return "", 0, ErrInvalid
	}
	var id string
	var done bool
	if err := s.Commit(ctx, func(tx store.Tx) error {
		t, sc, err := scope(tx)
		if err != nil {
			return err
		}
		var state string
		var members int
		switch err = t.QueryRow("SELECT id, state, members FROM review_decision WHERE request=?", r.Request).Scan(&id, &state, &members); {
		case err == nil && state == "committed":
			done = true
			return nil
		case err == nil: // a pending attempt of the same request is restarted from nothing
			if _, err = sc.Exec("DELETE FROM review_member WHERE decision=?", id); err != nil {
				return err
			}
			_, err = sc.Exec("UPDATE review_decision SET members=0 WHERE id=?", id)
			return err
		}
		if id, err = newID(); err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO review_decision(id, request, batch, kind, keep, state, members) VALUES(?,?,?,'bulk',?,'pending',0)`,
			id, r.Request, r.Batch, boolInt(r.Keep))
		return err
	}); err != nil {
		return "", 0, wrap(err)
	}
	if done {
		var n int
		err := s.Commit(ctx, func(tx store.Tx) error {
			t, _, err := scope(tx)
			if err != nil {
				return err
			}
			return t.QueryRow("SELECT members FROM review_decision WHERE id=?", id).Scan(&n)
		})
		if err != nil {
			return "", 0, wrap(err)
		}
		return id, n, nil
	}
	total := 0
	err := s.walk(ctx, r.Scope, func(chunk []findings.Finding) error {
		total += len(chunk)
		return s.Commit(ctx, func(tx store.Tx) error {
			t, sc, err := scope(tx)
			if err != nil {
				return err
			}
			versions := map[string]uint64{}
			for _, f := range chunk {
				v, ok := versions[f.Job]
				if !ok {
					if v, err = s.Findings.ResultVersion(tx, f.Job); err != nil {
						return err
					}
					versions[f.Job] = v
				}
				if err = insertMember(t, sc, id, f, v); err != nil {
					return err
				}
			}
			_, err = sc.Exec("UPDATE review_decision SET members=members+? WHERE id=?", len(chunk), id)
			return err
		})
	})
	if err != nil {
		return "", 0, wrap(err)
	}
	if total == 0 { // an empty scope decides nothing: do not leave a pending record behind
		_ = s.Commit(ctx, func(tx store.Tx) error {
			_, sc, err := scope(tx)
			if err != nil {
				return err
			}
			_, err = sc.Exec("DELETE FROM review_decision WHERE id=? AND state='pending'", id)
			return err
		})
		return "", 0, ErrInvalid
	}
	if err = s.Commit(ctx, func(tx store.Tx) error { return s.finish(tx, id, r.Replace) }); err != nil {
		return "", 0, wrap(err)
	}
	return id, total, nil
}

// finish is the commit of phase two. Everything it checks and writes is in one
// transaction, so a decision is either fully effective or not at all.
func (s *Service) finish(tx store.Tx, id string, replace bool) error {
	t, sc, err := scope(tx)
	if err != nil {
		return err
	}
	rows, err := t.Query("SELECT DISTINCT job, result FROM review_member WHERE decision=?", id)
	if err != nil {
		return err
	}
	type jv struct {
		job string
		v   uint64
	}
	var seen []jv
	for rows.Next() {
		var x jv
		if err = rows.Scan(&x.job, &x.v); err != nil {
			rows.Close()
			return err
		}
		seen = append(seen, x)
	}
	rows.Close()
	for _, x := range seen {
		cur, err := s.Findings.ResultVersion(tx, x.job)
		if err != nil {
			return err
		}
		if cur != x.v {
			return ErrScopeChanged // a member was read against another detection result
		}
	}
	if replace {
		rows, err = t.Query(`SELECT DISTINCT d.id FROM review_decision d
			JOIN review_member im ON im.decision=d.id
			JOIN review_member bm ON bm.job=im.job AND bm.unit=im.unit AND bm.text_version=im.text_version AND bm.start=im.start AND bm.stop=im.stop AND bm.category=im.category
			WHERE bm.decision=? AND d.kind='item' AND d.state='committed' AND d.revokes IS NULL
			AND NOT EXISTS (SELECT 1 FROM review_decision r WHERE r.revokes=d.id AND r.state='committed')`, id)
		if err != nil {
			return err
		}
		var victims []string
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			victims = append(victims, v)
		}
		rows.Close()
		for _, v := range victims {
			seq, err := nextSeq(t)
			if err != nil {
				return err
			}
			rid, err := newID()
			if err != nil {
				return err
			}
			if _, err = sc.Exec(`INSERT INTO review_decision(id, request, batch, kind, keep, state, seq, revokes, members)
				SELECT ?, ?, batch, kind, keep, 'committed', ?, id, 0 FROM review_decision WHERE id=?`, rid, "replace:"+id+":"+v, seq, v); err != nil {
				return err
			}
		}
	}
	seq, err := nextSeq(t)
	if err != nil {
		return err
	}
	res, err := sc.Exec("UPDATE review_decision SET state='committed', seq=? WHERE id=? AND state='pending'", seq, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	jobs := make([]string, 0, len(seen))
	for _, x := range seen {
		jobs = append(jobs, x.job)
	}
	sort.Strings(jobs)
	return bump(tx, t, sc, jobs)
}

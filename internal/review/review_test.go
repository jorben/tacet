package review

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type fx struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	fr    *findings.Repository
	hook  func(n int) error // called before each commit; may fail it
	calls int
	seq   int
}

func setup(t *testing.T) *fx {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "r.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(findings.Module(), Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fx{t: t, db: db}
	f.fr, err = findings.NewRepository(func(string) ([]byte, error) { return bytes.Repeat([]byte{7}, 32), nil })
	if err != nil {
		t.Fatal(err)
	}
	f.svc = &Service{Findings: f.fr, Chunk: 2, Commit: func(ctx context.Context, fn func(store.Tx) error) error {
		f.calls++
		if f.hook != nil {
			if err := f.hook(f.calls); err != nil {
				return err
			}
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		if err = fn(tx); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}}
	return f
}

func (f *fx) do(fn func(store.Tx) error) error {
	f.t.Helper()
	return f.svc.Commit(context.Background(), fn)
}

// detect adds and seals one detect attempt and returns the findings' ids in order.
func (f *fx) detect(job string, specs ...findings.New) []int64 {
	f.t.Helper()
	f.seq++
	a := kernel.AttemptRef{JobID: job, AttemptID: fmt.Sprintf("att%d", f.seq), OwnerGeneration: 1, JobVersion: 1}
	if err := f.do(func(tx store.Tx) error {
		if _, err := f.fr.Add(tx, a, "b1", findings.StageDetect, specs); err != nil {
			return err
		}
		_, err := f.fr.Seal(tx, a, "b1", findings.StageDetect)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	var ids []int64
	if err := f.do(func(tx store.Tx) error {
		page, err := f.fr.Page(tx, findings.Filter{Job: job}, 0, 200)
		for _, p := range page {
			if p.Attempt == a.AttemptID {
				ids = append(ids, p.ID)
			}
		}
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return ids
}

func nf(unit string, start int, cat string) findings.New {
	return findings.New{Unit: unit, TextVersion: 1, Start: start, End: start + 4, Category: cat, Source: kernel.SourceRule, Score: 0.9, Field: "body", Template: "t", Value: []byte("val" + fmt.Sprint(start))}
}

func (f *fx) page(job string) []findings.Finding {
	f.t.Helper()
	var out []findings.Finding
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.fr.Page(tx, findings.Filter{Job: job}, 0, 200); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fx) eff(job string) map[int64]Applied {
	f.t.Helper()
	var out map[int64]Applied
	fs := f.page(job)
	if err := f.do(func(tx store.Tx) (err error) { out, err = Effective(tx, fs); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fx) version(job string) uint64 {
	f.t.Helper()
	var v uint64
	if err := f.do(func(tx store.Tx) (err error) { v, err = Version(tx, job); return }); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func item(req, job string, id int64, keep bool) ItemRequest {
	return ItemRequest{Request: req, Batch: "b1", Job: job, Finding: id, Keep: keep}
}

func bulk(req string, keep, replace bool, sp ScopeSpec) BulkRequest {
	return BulkRequest{Request: req, Batch: "b1", Scope: sp, Keep: keep, Replace: replace}
}

func filterScope(job string) ScopeSpec {
	return ScopeSpec{Kind: ScopeFilter, Filter: findings.Filter{Job: job}}
}

func ctx() context.Context { return context.Background() }

func TestSingleDecisionIsEffectiveIdempotentAndBumpsTheVersion(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "email"))
	d1, err := f.svc.DecideItem(ctx(), item("r1", "j1", ids[0], false))
	if err != nil {
		t.Fatal(err)
	}
	if v := f.version("j1"); v != 1 {
		t.Fatalf("version %d", v)
	}
	d2, err := f.svc.DecideItem(ctx(), item("r1", "j1", ids[0], false))
	if err != nil || d2 != d1 || f.version("j1") != 1 {
		t.Fatalf("a retried request must change nothing: %q %q %v v%d", d1, d2, err, f.version("j1"))
	}
	e := f.eff("j1")
	if len(e) != 1 || e[ids[0]].Keep || e[ids[0]].Bulk || e[ids[0]].Decision != d1 {
		t.Fatalf("%+v", e)
	}
	if _, ok := e[ids[1]]; ok {
		t.Fatal("an undecided finding has a decision")
	}
	if _, err = f.svc.DecideItem(ctx(), item("r2", "j1", ids[1], true)); err != nil {
		t.Fatal(err)
	}
	if e = f.eff("j1"); !e[ids[1]].Keep || f.version("j1") != 2 {
		t.Fatalf("%+v v%d", e, f.version("j1"))
	}
}

func TestSingleDecisionRefusesUnknownFindingsOtherJobsAndBadRequests(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"))
	f.detect("j2", nf("u1", 0, "phone"))
	for name, r := range map[string]ItemRequest{
		"no request": item("", "j1", ids[0], false), "no job": item("r", "", ids[0], false), "no finding": item("r", "j1", 0, false),
		"long request": item(strings.Repeat("r", 129), "j1", ids[0], false), "no batch": {Request: "r", Job: "j1", Finding: ids[0]},
	} {
		if _, err := f.svc.DecideItem(ctx(), r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.DecideItem(ctx(), item("r", "j2", ids[0], false)); !errors.Is(err, ErrNotFound) {
		t.Errorf("a finding of another job: %v", err)
	}
	if _, err := f.svc.DecideItem(ctx(), item("r", "j1", 9999, false)); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing finding: %v", err)
	}
	if f.version("j1") != 0 || f.version("j2") != 0 {
		t.Error("a refused request bumped a version")
	}
}

func TestSingleBeatsBulkInEitherOrderAndBulkDoesNotOverrideByDefault(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"))
	// the bulk redacts all three, then the user keeps one by hand
	if _, n, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1"))); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := f.svc.DecideItem(ctx(), item("i1", "j1", ids[1], true)); err != nil {
		t.Fatal(err)
	}
	e := f.eff("j1")
	if !e[ids[0]].Bulk || e[ids[0]].Keep || e[ids[1]].Bulk || !e[ids[1]].Keep {
		t.Fatalf("a later single decision must win: %+v", e)
	}
	// a later bulk "keep" must not override the confirmed single decision
	imp, err := f.svc.ImpactOf(ctx(), filterScope("j1"))
	if err != nil || imp.Members != 3 || imp.ItemDecisions != 1 {
		t.Fatalf("%+v %v", imp, err)
	}
	if _, _, err = f.svc.DecideBulk(ctx(), bulk("b2", true, false, filterScope("j1"))); err != nil {
		t.Fatal(err)
	}
	e = f.eff("j1")
	if !e[ids[0]].Keep || !e[ids[0]].Bulk || !e[ids[1]].Keep || e[ids[1]].Bulk {
		t.Fatalf("%+v", e)
	}
	// with Replace the single decision is superseded and the bulk applies
	if _, _, err = f.svc.DecideBulk(ctx(), bulk("b3", false, true, filterScope("j1"))); err != nil {
		t.Fatal(err)
	}
	e = f.eff("j1")
	for _, id := range ids {
		if !e[id].Bulk || e[id].Keep {
			t.Fatalf("replace did not apply to %d: %+v", id, e[id])
		}
	}
	// the superseded decision stays in the history
	var n int
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT COUNT(*) FROM review_decision WHERE kind='item' AND revokes IS NULL").Scan(&n)
	})
	if n != 1 {
		t.Fatalf("history lost: %d item decisions", n)
	}
}

func TestLastCommittedBulkWinsAndLaterSingleWinsOverEarlier(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"))
	f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	f.svc.DecideBulk(ctx(), bulk("b2", true, false, filterScope("j1")))
	if e := f.eff("j1"); !e[ids[0]].Keep {
		t.Fatalf("the last bulk must win: %+v", e)
	}
	f.svc.DecideItem(ctx(), item("i1", "j1", ids[0], false))
	f.svc.DecideItem(ctx(), item("i2", "j1", ids[0], true))
	if e := f.eff("j1"); !e[ids[0]].Keep || e[ids[0]].Bulk {
		t.Fatalf("the last single must win: %+v", e)
	}
}

func TestScopeKindsMatchExactlyWhatTheyNameAndCountEqualsMembers(t *testing.T) {
	f := setup(t)
	a := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "email"), nf("u1", 27, "email"), nf("u1", 36, "phone"))
	b := f.detect("j2", nf("u1", 0, "phone"))
	phone := ScopeSpec{Kind: ScopeFilter, Filter: findings.Filter{Batch: "b1", Categories: []string{"phone"}}}
	if n, err := f.svc.Count(ctx(), phone); err != nil || n != 4 {
		t.Fatalf("filter count %d %v", n, err)
	}
	id, members, err := f.svc.DecideBulk(ctx(), bulk("bf", false, false, phone))
	if err != nil || members != 4 {
		t.Fatalf("%d %v", members, err)
	}
	for _, j := range []string{"j1", "j2"} {
		for fid, e := range f.eff(j) {
			if e.Decision != id {
				t.Fatalf("finding %d of %s: %+v", fid, j, e)
			}
		}
	}
	if e := f.eff("j1"); len(e) != 3 {
		t.Fatalf("only the phone findings of j1 are members: %d", len(e))
	}
	if _, ok := f.eff("j1")[a[2]]; ok {
		t.Fatal("an email finding is outside the phone filter")
	}
	if len(f.eff("j2")) != 1 || f.eff("j2")[b[0]].Decision != id {
		t.Fatal("a member of another job in the batch was missed")
	}
	// selected: exactly the ticked ones, across jobs
	sel := ScopeSpec{Kind: ScopeSelected, Refs: []Ref{{"j1", a[2]}, {"j2", b[0]}, {"j1", a[2]}}}
	if n, err := f.svc.Count(ctx(), sel); err != nil || n != 2 {
		t.Fatalf("a duplicated reference counts once: %d %v", n, err)
	}
	id2, _, err := f.svc.DecideBulk(ctx(), bulk("bs", true, false, sel))
	if err != nil {
		t.Fatal(err)
	}
	if e := f.eff("j1"); e[a[2]].Decision != id2 || e[a[3]].Decision != "" {
		t.Fatalf("%+v", e)
	}
	// page: exactly the references of the page
	pg := ScopeSpec{Kind: ScopePage, Refs: []Ref{{"j1", a[3]}}}
	if n, _ := f.svc.Count(ctx(), pg); n != 1 {
		t.Fatalf("page %d", n)
	}
}

func TestScopeRefusesEmptyOversizedForeignAndInvisibleReferences(t *testing.T) {
	f := setup(t)
	a := f.detect("j1", nf("u1", 0, "phone"))
	f.detect("j2", nf("u1", 0, "phone"))
	big := make([]Ref, MaxExplicit+1)
	for i := range big {
		big[i] = Ref{"j1", int64(i + 1)}
	}
	for name, sp := range map[string]ScopeSpec{
		"empty selected": {Kind: ScopeSelected}, "oversized": {Kind: ScopePage, Refs: big}, "no kind": {}, "unscoped filter": {Kind: ScopeFilter},
		"bad ref": {Kind: ScopeSelected, Refs: []Ref{{"", 1}}},
	} {
		if _, err := f.svc.Count(ctx(), sp); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
		if _, _, err := f.svc.DecideBulk(ctx(), bulk("x-"+name, false, false, sp)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s decide: %v", name, err)
		}
	}
	if _, err := f.svc.Count(ctx(), ScopeSpec{Kind: ScopeSelected, Refs: []Ref{{"j2", a[0]}}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a finding of another job: %v", err)
	}
	if _, _, err := f.svc.DecideBulk(ctx(), bulk("none", false, false, ScopeSpec{Kind: ScopeFilter, Filter: findings.Filter{Job: "j1", Categories: []string{"nothing"}}})); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty filter result: %v", err)
	}
	var pending int
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT COUNT(*) FROM review_decision").Scan(&pending)
	})
	if pending != 0 || f.version("j1") != 0 {
		t.Fatalf("refused scopes left %d records, version %d", pending, f.version("j1"))
	}
}

func TestAFindingAddedAfterTheDecisionIsNotAMember(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"))
	f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	late := f.detect("j1", nf("u1", 50, "phone"))
	if _, ok := f.eff("j1")[late[0]]; ok {
		t.Fatal("a hit added later joined the frozen scope")
	}
	if len(f.eff("j1")) != 1 {
		t.Fatalf("%v", f.eff("j1"))
	}
}

func TestScopeChangedDuringMaterialisationLeavesNothingEffective(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"), nf("u1", 27, "phone"))
	injected := false
	start := f.calls
	f.hook = func(n int) error {
		if !injected && n >= start+6 { // while members are being written
			injected = true
			h := f.hook
			f.hook = nil
			f.detect("j1", nf("u9", 0, "phone")) // a new finding changes the job's detection result
			f.hook = h
		}
		return nil
	}
	_, _, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	f.hook = nil
	if !injected {
		t.Fatal("the injection did not run")
	}
	if !errors.Is(err, ErrScopeChanged) {
		t.Fatalf("%v", err)
	}
	if len(f.eff("j1")) != 0 || f.version("j1") != 0 {
		t.Fatal("a half-built bulk decision is effective")
	}
	if n, err := f.svc.DropPending(ctx()); err != nil || n != 1 {
		t.Fatalf("pending %d %v", n, err)
	}
	// a retry after the change succeeds with the larger scope
	if _, n, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1"))); err != nil || n != 5 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestACrashBetweenThePhasesLeavesOnlyAPendingRecordThatNothingReads(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"))
	crash := errors.New("crash")
	start := f.calls
	f.hook = func(n int) error {
		if n == start+4 { // after the pending record and the first member chunk
			return crash
		}
		return nil
	}
	if _, _, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1"))); err == nil {
		t.Fatal("the crash was swallowed")
	}
	f.hook = nil
	if len(f.eff("j1")) != 0 || f.version("j1") != 0 {
		t.Fatal("a pending bulk decision took effect")
	}
	var members int
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT COUNT(*) FROM review_member").Scan(&members)
	})
	if members == 0 {
		t.Fatal("the test did not reach the member phase")
	}
	if n, err := f.svc.DropPending(ctx()); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT (SELECT COUNT(*) FROM review_member) + (SELECT COUNT(*) FROM review_decision)").Scan(&members)
	})
	if members != 0 {
		t.Fatalf("DropPending left %d rows", members)
	}
	// the same request id, retried, commits
	if _, n, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1"))); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestARetriedBulkRequestReturnsTheCommittedDecisionAndChangesNothing(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"))
	id1, n1, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	if err != nil {
		t.Fatal(err)
	}
	v := f.version("j1")
	id2, n2, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	if err != nil || id2 != id1 || n2 != n1 || f.version("j1") != v {
		t.Fatalf("%q %q %d %d %v", id1, id2, n1, n2, err)
	}
}

func TestRevokeKeepsHistoryCopiesNoMembersAndIsIdempotent(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"))
	bulkID, _, _ := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	itemID, _ := f.svc.DecideItem(ctx(), item("i1", "j1", ids[0], true))
	if err := f.svc.Revoke(ctx(), "u1", itemID); err != nil {
		t.Fatal(err)
	}
	if e := f.eff("j1"); e[ids[0]].Decision != bulkID {
		t.Fatalf("revoking the single decision must fall back to the bulk one: %+v", e[ids[0]])
	}
	v := f.version("j1")
	if err := f.svc.Revoke(ctx(), "u1b", itemID); err != nil || f.version("j1") != v {
		t.Fatalf("a repeated undo must change nothing: %v v%d->%d", err, v, f.version("j1"))
	}
	if err := f.svc.Revoke(ctx(), "u2", bulkID); err != nil {
		t.Fatal(err)
	}
	if len(f.eff("j1")) != 0 {
		t.Fatalf("a revoked bulk decision still applies: %v", f.eff("j1"))
	}
	var members, decisions, withMembers int
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		t.QueryRow("SELECT COUNT(*) FROM review_member").Scan(&members)
		t.QueryRow("SELECT COUNT(*) FROM review_decision").Scan(&decisions)
		return t.QueryRow("SELECT COUNT(*) FROM review_decision d JOIN review_member m ON m.decision=d.id WHERE d.revokes IS NOT NULL").Scan(&withMembers)
	})
	if members != 4 || decisions != 4 || withMembers != 0 {
		t.Fatalf("history or member copy: members=%d decisions=%d revoke-members=%d", members, decisions, withMembers)
	}
	for name, call := range map[string]func() error{
		"revoke a revocation": func() error { return f.svc.Revoke(ctx(), "u3", mustRevocation(f, bulkID)) },
		"unknown":             func() error { return f.svc.Revoke(ctx(), "u4", "dec_nope") },
		"no request":          func() error { return f.svc.Revoke(ctx(), "", bulkID) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func mustRevocation(f *fx, of string) string {
	var id string
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT id FROM review_decision WHERE revokes=?", of).Scan(&id)
	})
	return id
}

func TestRevokingAPendingDecisionIsRefused(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"))
	start := f.calls
	f.hook = func(n int) error {
		if n == start+4 {
			return errors.New("crash")
		}
		return nil
	}
	f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	f.hook = nil
	var id string
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT id FROM review_decision WHERE state='pending'").Scan(&id)
	})
	if id == "" {
		t.Fatal("no pending record")
	}
	if err := f.svc.Revoke(ctx(), "u1", id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestADecisionIsReusedOnlyForTheSameTextVersionSpanAndCategory(t *testing.T) {
	f := setup(t)
	first := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"))
	d, _, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	if err != nil {
		t.Fatal(err)
	}
	// a later detection finds the same hit again (new finding id), a same-value hit elsewhere,
	// the same span under another text version and the same span as another category
	same := nf("u1", 0, "phone")
	otherPlace := nf("u1", 40, "phone")
	otherPlace.Value = []byte("val0") // same value, new position
	otherVersion := nf("u1", 9, "phone")
	otherVersion.TextVersion = 2
	otherCategory := nf("u1", 9, "email")
	later := f.detect("j1", same, otherPlace, otherVersion, otherCategory)
	e := f.eff("j1")
	if e[later[0]].Decision != d {
		t.Fatalf("the same hit must keep its decision: %+v", e[later[0]])
	}
	for i, name := range []string{"", "same value at a new position", "another text version", "another category"} {
		if i == 0 {
			continue
		}
		if _, ok := e[later[i]]; ok {
			t.Errorf("%s inherited a decision", name)
		}
	}
	if e[first[0]].Decision != d || e[first[1]].Decision != d {
		t.Fatal("the original findings lost their decision")
	}
}

func TestPositionRoundTripsOverwritesAndIsBounded(t *testing.T) {
	f := setup(t)
	if _, ok, err := f.svc.LoadPosition(ctx(), "batch:b1"); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	p := Position{Filter: `{"c":["phone"]}`, Group: "g7", Cursor: "42"}
	if err := f.svc.SavePosition(ctx(), "batch:b1", p); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := f.svc.LoadPosition(ctx(), "batch:b1"); !ok || err != nil || got != p {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	p2 := Position{Cursor: "99"}
	f.svc.SavePosition(ctx(), "batch:b1", p2)
	if got, _, _ := f.svc.LoadPosition(ctx(), "batch:b1"); got != p2 {
		t.Fatalf("the latest save must win: %+v", got)
	}
	if got, ok, _ := f.svc.LoadPosition(ctx(), "batch:other"); ok || got != (Position{}) {
		t.Fatal("positions leak between keys")
	}
	for name, call := range map[string]func() error{
		"empty key": func() error { return f.svc.SavePosition(ctx(), "", p) },
		"long key":  func() error { return f.svc.SavePosition(ctx(), strings.Repeat("k", 257), p) },
		"long filter": func() error {
			return f.svc.SavePosition(ctx(), "k", Position{Filter: strings.Repeat("f", maxPosition+1)})
		},
		"long cursor": func() error {
			return f.svc.SavePosition(ctx(), "k", Position{Cursor: strings.Repeat("c", maxPosition+1)})
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAddManualIsAtomicIdempotentAndEffective(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"))
	r := ManualRequest{Request: "m1", Batch: "b1", Job: "j1", Unit: "u2", Version: 1, Start: 5, End: 9, Category: "custom", Value: []byte("abcd")}
	fid, did, err := f.svc.AddManual(ctx(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.Value = []byte("abcd")
	fid2, did2, err := f.svc.AddManual(ctx(), r)
	if err != nil || fid2 != fid || did2 != did {
		t.Fatalf("a retry must return the same finding and decision: %d %d %q %q %v", fid, fid2, did, did2, err)
	}
	if n := len(f.page("j1")); n != 2 {
		t.Fatalf("%d findings", n)
	}
	e := f.eff("j1")
	if e[fid].Keep || e[fid].Bulk || e[fid].Decision != did {
		t.Fatalf("the manual selection must be a single redact decision: %+v", e[fid])
	}
	// the decision must not outlive a failed finding: an invalid span stores neither
	bad := ManualRequest{Request: "m2", Batch: "b1", Job: "j1", Unit: "u2", Version: 1, Start: 9, End: 9, Category: "custom"}
	if _, _, err = f.svc.AddManual(ctx(), bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if n := len(f.page("j1")); n != 2 || len(f.eff("j1")) != 1 {
		t.Fatal("a failed manual selection left a finding or a decision")
	}
	for name, x := range map[string]ManualRequest{"no request": {Batch: "b1", Job: "j1", Unit: "u", Category: "c", End: 2}, "no category": {Request: "z", Batch: "b1", Job: "j1", Unit: "u", End: 2}} {
		if _, _, err = f.svc.AddManual(ctx(), x); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPurgeRemovesAJobsMembersAndVersionButNotAnotherJobs(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"))
	f.detect("j2", nf("u1", 0, "phone"))
	f.svc.DecideBulk(ctx(), bulk("b1", false, false, ScopeSpec{Kind: ScopeFilter, Filter: findings.Filter{Batch: "b1"}}))
	if err := f.do(func(tx store.Tx) error { return Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	if len(f.eff("j1")) != 0 || f.version("j1") != 0 {
		t.Fatal("j1 survived its purge")
	}
	if len(f.eff("j2")) != 1 || f.version("j2") != 1 {
		t.Fatal("j2 lost data to j1's purge")
	}
	if err := f.do(func(tx store.Tx) error { return Purge(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}

func TestServiceRefusesToRunUnwired(t *testing.T) {
	var s *Service
	if _, err := s.DecideItem(ctx(), ItemRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := (&Service{}).Count(ctx(), ScopeSpec{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestReplaceRevokesOnlySingleDecisionsNeverAnotherBulkDecisionWithOtherMembers(t *testing.T) {
	f := setup(t)
	ids := f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"), nf("u1", 18, "phone"))
	a, _, _ := f.svc.DecideBulk(ctx(), bulk("a", false, false, filterScope("j1")))
	one := ScopeSpec{Kind: ScopeSelected, Refs: []Ref{{"j1", ids[0]}}}
	b, _, err := f.svc.DecideBulk(ctx(), bulk("b", true, true, one))
	if err != nil {
		t.Fatal(err)
	}
	e := f.eff("j1")
	if e[ids[0]].Decision != b || e[ids[1]].Decision != a || e[ids[2]].Decision != a {
		t.Fatalf("replace damaged a bulk decision that reaches beyond its scope: %+v", e)
	}
}

func TestADecisionDroppedBetweenThePhasesCannotBeCommitted(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"), nf("u1", 9, "phone"))
	dropped := false
	start := f.calls
	f.hook = func(n int) error {
		if !dropped && n >= start+5 { // after the member phase, before the final commit
			dropped = true
			h := f.hook
			f.hook = nil
			f.svc.DropPending(ctx())
			f.hook = h
		}
		return nil
	}
	_, _, err := f.svc.DecideBulk(ctx(), bulk("b1", false, false, filterScope("j1")))
	f.hook = nil
	if !dropped || !errors.Is(err, ErrNotFound) {
		t.Fatalf("dropped=%v %v", dropped, err)
	}
	if len(f.eff("j1")) != 0 || f.version("j1") != 0 {
		t.Fatal("a dropped decision took effect")
	}
}

// FR-22 undo of a manual redaction: the selection and its decision go together, in one commit; a finding
// that is not manual is refused and nothing changes; the decision's history stays.
func TestWithdrawingAManualRedactionTakesTheFindingAndItsDecisionBack(t *testing.T) {
	f := setup(t)
	f.detect("j1", nf("u1", 0, "phone"))
	rule := f.page("j1")[0].ID
	fid, did, err := f.svc.AddManual(ctx(), ManualRequest{Request: "m1", Batch: "b1", Job: "j1", Unit: "u2", Version: 1, Start: 5, End: 9, Category: "custom", Value: []byte("abcd")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.DecideItem(ctx(), ItemRequest{Request: "d1", Batch: "b1", Job: "j1", Finding: rule, Keep: true}); err != nil {
		t.Fatal(err)
	}
	before := f.version("j1")
	if err = f.svc.WithdrawManual(ctx(), "w0", "j1", rule); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a detected finding is not withdrawn: %v", err)
	}
	if len(f.page("j1")) != 2 || f.version("j1") != before || len(f.eff("j1")) != 2 {
		t.Fatal("a refused withdrawal changed something")
	}
	if err = f.svc.WithdrawManual(ctx(), "w1", "j1", fid); err != nil {
		t.Fatal(err)
	}
	if got := f.page("j1"); len(got) != 1 || got[0].ID != rule {
		t.Fatalf("%+v", got)
	}
	e := f.eff("j1")
	if _, ok := e[fid]; ok || !e[rule].Keep || f.version("j1") <= before {
		t.Fatalf("the manual decision is revoked, the other one stays: %+v v%d", e, f.version("j1"))
	}
	var revoked int
	f.do(func(tx store.Tx) error {
		t, _, _ := scope(tx)
		return t.QueryRow("SELECT COUNT(*) FROM review_decision WHERE revokes=? AND state='committed'", did).Scan(&revoked)
	})
	if revoked != 1 {
		t.Fatalf("the decision's history is kept as a revocation: %d", revoked)
	}
	if err = f.svc.WithdrawManual(ctx(), "w2", "j1", fid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("withdrawn twice: %v", err)
	}
}

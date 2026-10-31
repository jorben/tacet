package exceptions

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type fx struct {
	t  *testing.T
	db *store.DB
	r  Repository
}

func setup(t *testing.T) *fx {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "e.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, err := store.Open(ctx, raw, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &fx{t: t, db: db}
}

func (f *fx) do(fn func(store.Tx) error) error {
	f.t.Helper()
	tx, err := f.db.Begin(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if err = fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (f *fx) sync(job string, res string, ds ...Draft) SyncResult {
	f.t.Helper()
	var out SyncResult
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.r.Sync(tx, job, ds, res); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fx) items(flt Filter) []Row {
	f.t.Helper()
	var out []Row
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.r.Items(tx, flt, "", 200); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func d(t Type, scope, reason string) Draft { return Draft{Type: t, ScopeKey: scope, Reason: reason} }

func jobs(j ...string) []string { return j }

var eligible = func(Row) Context { return Context{PartialEligible: true} }

func TestSyncKeepsIdentityAddsReopensAndResolvesAndIsIdempotent(t *testing.T) {
	f := setup(t)
	r := f.sync("j1", "v1", d(ScanPage, "part:p1", "scan_page"), d(UnreliableLocation, "u1:0-4", "unreliable_location"))
	if r != (SyncResult{Added: 2}) {
		t.Fatalf("%+v", r)
	}
	if r = f.sync("j1", "v1", d(ScanPage, "part:p1", "scan_page"), d(UnreliableLocation, "u1:0-4", "unreliable_location")); r != (SyncResult{}) {
		t.Fatalf("a repeated sync must change nothing: %+v", r)
	}
	id := ItemIDFor("j1", ScanPage, "part:p1")
	if n, err := f.decide("r1", jobs("j1"), []string{id}, Redact); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// resolution 2: the unreliable location is gone, the scan page stays, a new one appears
	r = f.sync("j1", "v2", d(ScanPage, "part:p1", "scan_page"), d(ImageWithText, "part:img", "image_with_text"))
	if r != (SyncResult{Added: 1, Resolved: 1}) {
		t.Fatalf("%+v", r)
	}
	all := f.items(Filter{Jobs: jobs("j1"), IncludeResolved: true})
	by := map[string]Row{}
	for _, x := range all {
		by[x.ID] = x
	}
	if !by[ItemIDFor("j1", ScanPage, "part:p1")].Decided || by[ItemIDFor("j1", ScanPage, "part:p1")].Resolved {
		t.Fatal("the surviving exception lost its decision")
	}
	if !by[ItemIDFor("j1", UnreliableLocation, "u1:0-4")].Resolved {
		t.Fatal("a vanished exception was not resolved")
	}
	var basis string
	f.do(func(tx store.Tx) error {
		tt, _, _ := scope(tx)
		return tt.QueryRow("SELECT basis FROM exceptions_item WHERE id=?", ItemIDFor("j1", UnreliableLocation, "u1:0-4")).Scan(&basis)
	})
	if basis != "v2" {
		t.Fatalf("resolution basis %q", basis)
	}
	if len(f.items(Filter{Jobs: jobs("j1")})) != 2 {
		t.Fatal("resolved items must be hidden by default")
	}
	// it comes back: same id, reopened, not new
	r = f.sync("j1", "v3", d(ScanPage, "part:p1", "scan_page"), d(UnreliableLocation, "u1:0-4", "unreliable_location"), d(ImageWithText, "part:img", "image_with_text"))
	if r != (SyncResult{Reopened: 1}) {
		t.Fatalf("%+v", r)
	}
	// a changed reason updates in place
	r = f.sync("j1", "v4", d(ScanPage, "part:p1", "scan_page_v2"), d(UnreliableLocation, "u1:0-4", "unreliable_location"), d(ImageWithText, "part:img", "image_with_text"))
	if r != (SyncResult{Updated: 1}) {
		t.Fatalf("%+v", r)
	}
	// another job's items are untouched by this job's sync
	f.sync("j2", "v1", d(ScanPage, "part:p1", "scan_page"))
	f.sync("j1", "v5")
	if len(f.items(Filter{Jobs: jobs("j2")})) != 1 {
		t.Fatal("j1's sync resolved j2's exception")
	}
}

func (f *fx) decide(req string, js, ids []string, a ActionKind) (int, error) {
	var n int
	err := f.do(func(tx store.Tx) (err error) { n, err = f.r.Decide(tx, req, js, ids, a, eligible); return })
	return n, err
}

func TestSyncRejectsUnknownTypesEmptyScopesAndBadArguments(t *testing.T) {
	f := setup(t)
	for name, c := range map[string]struct {
		job, res string
		ds       []Draft
	}{
		"unknown type": {"j", "v", []Draft{d("nope", "s", "r")}}, "empty scope": {"j", "v", []Draft{d(ScanPage, "", "r")}},
		"long scope": {"j", "v", []Draft{d(ScanPage, strings.Repeat("s", 1025), "r")}}, "long reason": {"j", "v", []Draft{d(ScanPage, "s", strings.Repeat("r", 129))}},
		"no job": {"", "v", nil}, "no resolution": {"j", "", nil},
	} {
		if err := f.do(func(tx store.Tx) error { _, err := f.r.Sync(tx, c.job, c.ds, c.res); return err }); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.r.Sync(tx, "j", []Draft{d(ScanPage, "ok", "r"), d("nope", "s", "r")}, "v")
		return err
	}); err == nil {
		t.Fatal("a bad draft in the list was accepted")
	}
	if len(f.items(Filter{Jobs: jobs("j")})) != 0 {
		t.Fatal("a rejected sync stored something")
	}
}

func TestDecideChecksAvailabilityGroupingScopeAndIsIdempotent(t *testing.T) {
	f := setup(t)
	f.sync("j1", "v", d(ScanPage, "a", "scan_page"), d(ScanPage, "b", "scan_page"), d(UnreliableLocation, "x", "u"), d(UnreliableLocation, "y", "u"), d(PasswordRequired, "file", "password"))
	a, b := ItemIDFor("j1", ScanPage, "a"), ItemIDFor("j1", ScanPage, "b")
	x, y := ItemIDFor("j1", UnreliableLocation, "x"), ItemIDFor("j1", UnreliableLocation, "y")
	if n, err := f.decide("r1", jobs("j1"), []string{a, b}, Keep); err != nil || n != 2 {
		t.Fatalf("a groupable type may be decided together: %d %v", n, err)
	}
	if n, err := f.decide("r1", jobs("j1"), []string{a, b}, Keep); err != nil || n != 0 {
		t.Fatalf("a retried request must record nothing: %d %v", n, err)
	}
	if _, err := f.decide("r2", jobs("j1"), []string{x, y}, Redact); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("per-item types must not be decided as a group: %v", err)
	}
	if n, err := f.decide("r3", jobs("j1"), []string{x}, Redact); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := f.decide("r4", jobs("j1"), []string{x}, Skip); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("skip is not offered for content exceptions: %v", err)
	}
	pw := ItemIDFor("j1", PasswordRequired, "file")
	if _, err := f.decide("r5", jobs("j1"), []string{pw}, Keep); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	// keep is partial: refused when the file is not partial-eligible
	no := func(Row) Context { return Context{} }
	f.sync("j1", "v2", d(ScanPage, "a", "scan_page"), d(ScanPage, "b", "scan_page"), d(UnreliableLocation, "x", "u"), d(UnreliableLocation, "y", "u"), d(PasswordRequired, "file", "password"), d(EmbeddedObject, "e", "embedded"))
	e := ItemIDFor("j1", EmbeddedObject, "e")
	if err := f.do(func(tx store.Tx) error { _, err := f.r.Decide(tx, "r6", jobs("j1"), []string{e}, Keep, no); return err }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("keep must be refused without partial eligibility: %v", err)
	}
	// the consequence the user was shown is recorded with the decision
	dec := f.decisions("j1")
	if dec[a].Consequence != PartialResult || dec[a].Action != Keep {
		t.Fatalf("%+v", dec[a])
	}
	// unknown item, resolved item, another job's item
	if _, err := f.decide("r7", jobs("j1"), []string{"ex_nope"}, Redact); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := f.decide("r8", jobs("j2"), []string{e}, Redact); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an item of a job outside the scope: %v", err)
	}
	f.sync("j1", "v3")
	if _, err := f.decide("r9", jobs("j1"), []string{e}, Redact); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a resolved item cannot be decided: %v", err)
	}
	for name, call := range map[string]func() error{
		"no request": func() error { _, err := f.decide("", jobs("j1"), []string{e}, Redact); return err },
		"no ids":     func() error { _, err := f.decide("q", jobs("j1"), nil, Redact); return err },
		"no jobs":    func() error { _, err := f.decide("q", nil, []string{e}, Redact); return err },
		"empty id":   func() error { _, err := f.decide("q", jobs("j1"), []string{""}, Redact); return err },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a failed batch records nothing: one bad item stops the whole call
	f.sync("j3", "v", d(ScanPage, "a", "r"), d(UnreliableLocation, "x", "u"), d(UnreliableLocation, "y", "u"))
	before := len(f.decisions("j3"))
	if _, err := f.decide("z", jobs("j3"), []string{ItemIDFor("j3", ScanPage, "a"), ItemIDFor("j3", UnreliableLocation, "x")}, Redact); !errors.Is(err, ErrNoGroup) {
		t.Fatal(err)
	}
	if len(f.decisions("j3")) != before {
		t.Fatal("a refused call recorded decisions")
	}
}

func (f *fx) decisions(job string) map[string]Decision {
	f.t.Helper()
	var out map[string]Decision
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.r.Decisions(tx, job); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestGroupDecisionIsFrozenToTheCountTheUserSaw(t *testing.T) {
	f := setup(t)
	f.sync("j1", "v", d(ScanPage, "a", "scan"), d(ScanPage, "b", "scan"), d(ScanPage, "c", "other"))
	f.sync("j2", "v", d(ScanPage, "a", "scan"))
	flt := Filter{Jobs: jobs("j1", "j2"), Type: ScanPage, Reason: "scan"}
	var groups []GroupRow
	f.do(func(tx store.Tx) (err error) { groups, err = f.r.Groups(tx, Filter{Jobs: jobs("j1", "j2")}); return })
	if len(groups) != 2 || groups[0].Reason != "other" || groups[0].Items != 1 || groups[1].Reason != "scan" || groups[1].Items != 3 || groups[1].Files != 2 || groups[1].Decided != 0 {
		t.Fatalf("%+v", groups)
	}
	// the group changed after the user looked: a new member appeared
	f.sync("j1", "v2", d(ScanPage, "a", "scan"), d(ScanPage, "b", "scan"), d(ScanPage, "c", "other"), d(ScanPage, "d", "scan"))
	err := f.do(func(tx store.Tx) error { _, err := f.r.DecideGroup(tx, "g1", flt, 3, Redact, eligible); return err })
	if !errors.Is(err, ErrScope) {
		t.Fatalf("%v", err)
	}
	if len(f.decisions("j1"))+len(f.decisions("j2")) != 0 {
		t.Fatal("a stale group decision recorded something")
	}
	var n int
	if err = f.do(func(tx store.Tx) (e error) { n, e = f.r.DecideGroup(tx, "g2", flt, 4, Redact, eligible); return }); err != nil || n != 4 {
		t.Fatalf("%d %v", n, err)
	}
	f.do(func(tx store.Tx) (err error) { groups, err = f.r.Groups(tx, Filter{Jobs: jobs("j1", "j2")}); return })
	if groups[1].Decided != 4 || groups[0].Decided != 0 {
		t.Fatalf("%+v", groups)
	}
	// the decided ones are no longer part of an undecided group
	if err = f.do(func(tx store.Tx) error { _, err := f.r.DecideGroup(tx, "g3", flt, 4, Redact, eligible); return err }); !errors.Is(err, ErrScope) {
		t.Fatalf("%v", err)
	}
	if err = f.do(func(tx store.Tx) error {
		_, err := f.r.DecideGroup(tx, "g4", Filter{Jobs: jobs("j1")}, 0, Redact, eligible)
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if err = f.do(func(tx store.Tx) error {
		_, err := f.r.DecideGroup(tx, "g5", Filter{}, 1, Redact, eligible)
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unscoped group decision: %v", err)
	}
}

func TestRevokeKeepsHistoryAndTheLastEffectiveDecisionWins(t *testing.T) {
	f := setup(t)
	f.sync("j1", "v", d(ScanPage, "a", "scan"))
	id := ItemIDFor("j1", ScanPage, "a")
	f.decide("r1", jobs("j1"), []string{id}, Keep)
	f.decide("r2", jobs("j1"), []string{id}, Redact)
	if got := f.decisions("j1")[id]; got.Action != Redact {
		t.Fatalf("the latest decision must win: %+v", got)
	}
	revoke := func(req string) bool {
		var done bool
		if err := f.do(func(tx store.Tx) (err error) { done, err = f.r.Revoke(tx, req, jobs("j1"), id); return }); err != nil {
			t.Fatal(err)
		}
		return done
	}
	if !revoke("u1") {
		t.Fatal("revoke did nothing")
	}
	if got := f.decisions("j1")[id]; got.Action != Keep {
		t.Fatalf("revoking the latest must fall back to the earlier decision: %+v", got)
	}
	if !revoke("u2") || revoke("u3") {
		t.Fatal("second revoke should withdraw the earlier decision, third has nothing left")
	}
	if len(f.decisions("j1")) != 0 {
		t.Fatal("a revoked item still has a decision")
	}
	var open int
	f.do(func(tx store.Tx) (err error) { open, err = f.r.Open(tx, "j1"); return })
	if open != 1 {
		t.Fatalf("an item without a decision is open: %d", open)
	}
	var rows int
	f.do(func(tx store.Tx) error {
		tt, _, _ := scope(tx)
		return tt.QueryRow("SELECT COUNT(*) FROM exceptions_decision").Scan(&rows)
	})
	if rows != 4 {
		t.Fatalf("history rows %d", rows)
	}
	// repeating a revoke request id is harmless
	f.decide("r3", jobs("j1"), []string{id}, Keep)
	if !revoke("u9") || revoke("u9") {
		t.Fatal("a repeated revoke request must record nothing the second time")
	}
	for name, call := range map[string]func() error{
		"unknown":  func() error { _, err := f.r.Revoke(nil, "x", jobs("j1"), "ex_nope"); return err },
		"no req":   func() error { _, err := f.r.Revoke(nil, "", jobs("j1"), id); return err },
		"no jobs":  func() error { _, err := f.r.Revoke(nil, "x", nil, id); return err },
		"empty id": func() error { _, err := f.r.Revoke(nil, "x", jobs("j1"), ""); return err },
	} {
		if name == "unknown" {
			if err := f.do(func(tx store.Tx) error { _, err := f.r.Revoke(tx, "x", jobs("j1"), "ex_nope"); return err }); !errors.Is(err, ErrNotFound) {
				t.Errorf("%v", err)
			}
			continue
		}
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.r.Revoke(tx, "x", jobs("j2"), id); return err }); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking through another job's scope: %v", err)
	}
}

func TestItemsPageByKeysetAndRefuseUnscopedOrOversizedQueries(t *testing.T) {
	f := setup(t)
	var ds []Draft
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		ds = append(ds, d(ScanPage, s, "scan"))
	}
	f.sync("j1", "v", ds...)
	var p1, p2 []Row
	f.do(func(tx store.Tx) (err error) { p1, err = f.r.Items(tx, Filter{Jobs: jobs("j1")}, "", 3); return })
	f.do(func(tx store.Tx) (err error) { p2, err = f.r.Items(tx, Filter{Jobs: jobs("j1")}, p1[2].ID, 3); return })
	seen := map[string]bool{}
	for _, x := range append(p1, p2...) {
		seen[x.ID] = true
	}
	if len(p1) != 3 || len(p2) != 2 || len(seen) != 5 {
		t.Fatalf("%d %d %d", len(p1), len(p2), len(seen))
	}
	for name, c := range map[string]struct {
		f     Filter
		limit int
	}{"no jobs": {Filter{}, 5}, "limit 0": {Filter{Jobs: jobs("j1")}, 0}, "limit too big": {Filter{Jobs: jobs("j1")}, MaxPage + 1}, "empty job": {Filter{Jobs: jobs("")}, 5}} {
		if err := f.do(func(tx store.Tx) error { _, err := f.r.Items(tx, c.f, "", c.limit); return err }); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.r.Items(tx, Filter{Jobs: jobs("j1"), Type: "bogus"}, "", 5)
		return err
	}); !errors.Is(err, ErrUnknownType) {
		t.Errorf("%v", err)
	}
	// only undecided
	f.decide("r", jobs("j1"), []string{ItemIDFor("j1", ScanPage, "a")}, Redact)
	if n := len(f.items(Filter{Jobs: jobs("j1"), OnlyUndecided: true})); n != 4 {
		t.Fatalf("%d", n)
	}
}

func (f *fx) version(job string) uint64 {
	f.t.Helper()
	var v uint64
	if err := f.do(func(tx store.Tx) (err error) { v, err = f.r.Version(tx, job); return }); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func TestEveryRecordedDecisionOrRevocationRaisesTheJobVersionAndNothingElseDoes(t *testing.T) {
	f := setup(t)
	f.sync("j1", "v", d(ScanPage, "a", "scan"), d(ScanPage, "b", "scan"))
	f.sync("j2", "v", d(ScanPage, "a", "scan"))
	a, b := ItemIDFor("j1", ScanPage, "a"), ItemIDFor("j1", ScanPage, "b")
	if f.version("j1") != 0 {
		t.Fatal("syncing is not a decision")
	}
	f.decide("r1", jobs("j1"), []string{a, b}, Redact)
	if f.version("j1") != 1 || f.version("j2") != 0 {
		t.Fatalf("v1=%d v2=%d", f.version("j1"), f.version("j2"))
	}
	f.decide("r1", jobs("j1"), []string{a, b}, Redact) // a retry records nothing
	if f.version("j1") != 1 {
		t.Fatal("a retried request raised the version")
	}
	f.do(func(tx store.Tx) error { _, err := f.r.Revoke(tx, "u1", jobs("j1"), a); return err })
	if f.version("j1") != 2 {
		t.Fatal("a revocation did not raise the version")
	}
	f.do(func(tx store.Tx) error {
		_, err := f.r.Revoke(tx, "u2", jobs("j1"), ItemIDFor("j1", ScanPage, "never"))
		return err
	})
	var done bool
	f.do(func(tx store.Tx) (err error) { done, err = f.r.Revoke(tx, "u3", jobs("j1"), a); return })
	if done || f.version("j1") != 2 {
		t.Fatal("a revocation with nothing to revoke raised the version")
	}
	f.do(func(tx store.Tx) error {
		_, err := f.r.DecideGroup(tx, "g", Filter{Jobs: jobs("j2")}, 1, Keep, eligible)
		return err
	})
	if f.version("j2") != 1 {
		t.Fatal("a group decision did not raise the version")
	}
	f.do(func(tx store.Tx) error { return f.r.Purge(tx, "j1") })
	if f.version("j1") != 0 || f.version("j2") != 1 {
		t.Fatal("purge must reset only its own job")
	}
	if err := f.do(func(tx store.Tx) error { _, err := f.r.Version(tx, ""); return err }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestPurgeRemovesAJobsItemsAndHistoryOnly(t *testing.T) {
	f := setup(t)
	f.sync("j1", "v", d(ScanPage, "a", "scan"))
	f.sync("j2", "v", d(ScanPage, "a", "scan"))
	f.decide("r1", jobs("j1"), []string{ItemIDFor("j1", ScanPage, "a")}, Redact)
	// same scope key in two jobs gives the same item id: purge must not take j2's row
	if err := f.do(func(tx store.Tx) error { return f.r.Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	if len(f.items(Filter{Jobs: jobs("j1")})) != 0 {
		t.Fatal("j1 survived")
	}
	if got := f.items(Filter{Jobs: jobs("j2")}); len(got) != 1 || got[0].ID != ItemIDFor("j2", ScanPage, "a") || got[0].ID == ItemIDFor("j1", ScanPage, "a") {
		t.Fatalf("j2 lost its item or shares an id with j1: %+v", got)
	}
	if err := f.do(func(tx store.Tx) error { return f.r.Purge(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func (f *fx) syncPost(job, v string, ds ...Draft) SyncResult {
	f.t.Helper()
	var out SyncResult
	if err := f.do(func(tx store.Tx) (err error) { out, err = f.r.SyncPostExport(tx, job, ds, v); return }); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestExceptionsFoundAfterExportSurviveReResolutionAndAreResolvedOnlyByValidation(t *testing.T) {
	f := setup(t)
	f.sync("j1", "r1", d(ScanPage, "part:p", "scan_page"))
	if r := f.syncPost("j1", "val1", d(PolicyReview, "gen:u1:0-5", "generated_content_hit"), d(UnreliableLocation, "cand:u2:1-3", "unlocated")); r != (SyncResult{Added: 2}) {
		t.Fatalf("%+v", r)
	}
	gen := ItemIDFor("j1", PolicyReview, "gen:u1:0-5")
	if r := f.syncPost("j1", "val1", d(PolicyReview, "gen:u1:0-5", "generated_content_hit"), d(UnreliableLocation, "cand:u2:1-3", "unlocated")); r != (SyncResult{}) {
		t.Fatalf("repeat: %+v", r)
	}
	// a re-resolution that does not know these items neither resolves nor flags them
	if r := f.sync("j1", "r2", d(ScanPage, "part:p", "scan_page")); r != (SyncResult{}) {
		t.Fatalf("re-resolution touched post-export items: %+v", r)
	}
	open := f.items(Filter{Jobs: jobs("j1")})
	if len(open) != 3 {
		t.Fatalf("%+v", open)
	}
	for _, x := range open {
		if x.ID == gen && !x.PostExport {
			t.Fatal("flag lost")
		}
	}
	// a normal draft with the same identity does not clear the flag either
	f.sync("j1", "r3", d(ScanPage, "part:p", "scan_page"), Draft{Type: PolicyReview, ScopeKey: "gen:u1:0-5", Reason: "other"})
	for _, x := range f.items(Filter{Jobs: jobs("j1")}) {
		if x.ID == gen && !x.PostExport {
			t.Fatal("a normal sync cleared the post-export flag")
		}
	}
	// a decision survives a later validation that still finds the item
	f.decide("r", jobs("j1"), []string{gen}, Redact)
	f.syncPost("j1", "val2", d(PolicyReview, "gen:u1:0-5", "generated_content_hit"))
	if !f.items(Filter{Jobs: jobs("j1"), IncludeResolved: true})[0].Decided && f.decisions("j1")[gen].Action != Redact {
		t.Fatal("decision lost")
	}
	// the later validation no longer finds the unlocated one: only it is resolved
	all := f.items(Filter{Jobs: jobs("j1"), IncludeResolved: true})
	resolved := 0
	for _, x := range all {
		if x.Resolved {
			resolved++
			if x.ID == gen || x.Type != UnreliableLocation {
				t.Fatalf("wrong item resolved: %+v", x)
			}
		}
	}
	if resolved != 1 {
		t.Fatalf("%d resolved", resolved)
	}
	// it comes back
	if r := f.syncPost("j1", "val3", d(PolicyReview, "gen:u1:0-5", "generated_content_hit"), d(UnreliableLocation, "cand:u2:1-3", "unlocated")); r.Reopened != 1 {
		t.Fatalf("%+v", r)
	}
	// a clean validation resolves them all, and the normal items stay open
	if r := f.syncPost("j1", "val4"); r.Resolved != 2 {
		t.Fatalf("%+v", r)
	}
	if n := len(f.items(Filter{Jobs: jobs("j1")})); n != 1 {
		t.Fatalf("%d open", n)
	}
	for name, call := range map[string]func() error{
		"no job":     func() error { _, err := f.r.SyncPostExport(nil, "", nil, "v"); return err },
		"no version": func() error { _, err := f.r.SyncPostExport(nil, "j", nil, ""); return err },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error {
		_, err := f.r.SyncPostExport(tx, "j1", []Draft{{Type: "nope", ScopeKey: "s"}}, "v")
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown type: %v", err)
	}
}

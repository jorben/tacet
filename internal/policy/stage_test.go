package policy

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/exceptions"
	"github.com/jorben/tacet/internal/findings"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/store"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type sfx struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	rev   *review.Service
	hook  func(n int)
	calls int
	seq   int
}

func ssetup(t *testing.T) *sfx {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "p.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(findings.Module(), review.Module(), exceptions.Module(), coverage.Module(), Module())
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
	f := &sfx{t: t, db: db}
	fr, err := findings.NewRepository(func(string) ([]byte, error) { return bytes.Repeat([]byte{3}, 32), nil })
	if err != nil {
		t.Fatal(err)
	}
	commit := func(ctx context.Context, fn func(store.Tx) error) error {
		f.calls++
		if f.hook != nil {
			f.hook(f.calls)
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
	}
	f.svc = &Service{Commit: commit, Findings: fr}
	f.rev = &review.Service{Commit: commit, Findings: fr, Chunk: 50}
	return f
}

func (f *sfx) do(fn func(store.Tx) error) error { return f.svc.Commit(context.Background(), fn) }

func (f *sfx) detect(job string, specs ...findings.New) []int64 {
	f.t.Helper()
	f.seq++
	a := kernel.AttemptRef{JobID: job, AttemptID: "att" + strconv.Itoa(f.seq), OwnerGeneration: 1, JobVersion: 1}
	var ids []int64
	if err := f.do(func(tx store.Tx) error {
		if _, err := f.svc.Findings.Add(tx, a, "b1", findings.StageDetect, specs); err != nil {
			return err
		}
		if _, err := f.svc.Findings.Seal(tx, a, "b1", findings.StageDetect); err != nil {
			return err
		}
		page, err := f.svc.Findings.Page(tx, findings.Filter{Job: job}, 0, 200)
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

func nf(unit string, start int, cat string, src kernel.FindingSource, score float64) findings.New {
	return findings.New{Unit: unit, TextVersion: 1, Start: start, End: start + 4, Category: cat, Source: src, Score: score, Field: "body", Template: "t",
		Value: []byte("v" + strconv.Itoa(start))}
}

func (f *sfx) ledger(job string, parts ...coverage.Part) {
	f.t.Helper()
	if err := f.do(func(tx store.Tx) error {
		var r coverage.Repository
		if err := r.Begin(tx, job, "led-"+job); err != nil {
			return err
		}
		for _, p := range parts {
			if err := r.Record(tx, job, "led-"+job, p); err != nil {
				return err
			}
		}
		return r.Complete(tx, job, "led-"+job)
	}); err != nil {
		f.t.Fatal(err)
	}
}

func scanPart(id string) coverage.Part {
	return coverage.Part{ID: id, Class: coverage.ClassScanPage, Required: true, Known: true, Bounded: true, Inspection: coverage.NotInspected, Disposition: coverage.Pending, Validation: coverage.NotRun}
}

func textPart(id string) coverage.Part {
	return coverage.Part{ID: id, Class: coverage.ClassText, Required: true, Known: true, Bounded: true, Inspection: coverage.Inspected, Disposition: coverage.Pending, Validation: coverage.NotRun}
}

func req(job string) Request { return Request{Job: job, Policy: base(), DisposeDigest: "dig1"} }

func (f *sfx) stored(job string) (Stored, bool, []Disposition) {
	f.t.Helper()
	var s Stored
	var ok bool
	var ds []Disposition
	if err := f.do(func(tx store.Tx) (err error) {
		if s, ok, err = Resolution(tx, job); err != nil {
			return err
		}
		ds, err = Dispositions(tx, job, 0, MaxPage)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return s, ok, ds
}

func (f *sfx) open(job string) int {
	var n int
	f.do(func(tx store.Tx) (err error) { n, err = (exceptions.Repository{}).Open(tx, job); return })
	return n
}

func TestRunStoresDispositionsExceptionsAndTheKeyAndIsRepeatable(t *testing.T) {
	f := ssetup(t)
	ids := f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1), nf("u1", 20, "person", kernel.SourceModel, 0.95), nf("u1", 40, "person", kernel.SourceModel, 0.3))
	f.ledger("j1", textPart("doc"), scanPart("page2"))
	r, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Dispositions != 2 || r.Stats.BelowMinConfidence != 1 || r.Drafts != 1 || r.Sync.Added != 1 || r.Attempts != 1 || r.DetectVersion != 1 {
		t.Fatalf("%+v", r)
	}
	s, ok, ds := f.stored("j1")
	if !ok || s.Key != r.Key || s.Dispositions != 2 || s.Stats.BelowMinConfidence != 1 || s.DisposeDigest != "dig1" || len(ds) != 2 {
		t.Fatalf("%+v %v %+v", s, ok, ds)
	}
	if ds[0].Unit != "u1" || ds[0].Start != 0 || ds[0].Action != Delete || ds[0].Category != "phone" || ds[1].Action != Placeholder {
		t.Fatalf("%+v", ds)
	}
	if f.open("j1") != 1 {
		t.Fatal("the scan page gap must be an open exception")
	}
	r2, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil || r2.Key != r.Key || r2.Sync != (exceptions.SyncResult{}) {
		t.Fatalf("a repeated run must change nothing: %+v %v", r2, err)
	}
	// a different dispose facet is a different resolution
	q := req("j1")
	q.DisposeDigest = "dig2"
	r3, err := f.svc.Run(context.Background(), q)
	if err != nil || r3.Key == r.Key {
		t.Fatalf("%+v %v", r3, err)
	}
	_ = ids
}

func TestAHumanDecisionChangesTheResolutionAndRaisesTheKey(t *testing.T) {
	f := ssetup(t)
	ids := f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1), nf("u1", 20, "person", kernel.SourceModel, 0.3))
	f.ledger("j1", textPart("doc"))
	r1, _ := f.svc.Run(context.Background(), req("j1"))
	if r1.Dispositions != 1 {
		t.Fatalf("%+v", r1)
	}
	// the user redacts the candidate below the minimum confidence (MD-12), and keeps the phone
	if _, err := f.rev.DecideItem(context.Background(), review.ItemRequest{Request: "i1", Batch: "b1", Job: "j1", Finding: ids[1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rev.DecideItem(context.Background(), review.ItemRequest{Request: "i2", Batch: "b1", Job: "j1", Finding: ids[0], Keep: true}); err != nil {
		t.Fatal(err)
	}
	r2, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Key == r1.Key || r2.DecisionVersion != 2 || r2.Stats.HumanItem != 2 || r2.Stats.BelowMinConfidence != 0 {
		t.Fatalf("%+v", r2)
	}
	_, _, ds := f.stored("j1")
	by := map[string]Disposition{}
	for _, d := range ds {
		by[d.Category] = d
	}
	if by["phone"].Action != Keep || by["phone"].Basis != HumanItem || by["person"].Action != Placeholder || by["person"].Basis != HumanItem {
		t.Fatalf("%+v", ds)
	}
}

func TestADecisionOnAnExceptionAndOnAGapCountsAndClosesThem(t *testing.T) {
	f := ssetup(t)
	f.detect("j1", func() findings.New { n := nf("u1", 0, "phone", kernel.SourceRule, 1); n.Mismatch = true; return n }())
	f.ledger("j1", textPart("doc"), scanPart("page2"))
	r, _ := f.svc.Run(context.Background(), req("j1"))
	if r.Drafts != 2 || r.Dispositions != 0 || f.open("j1") != 2 {
		t.Fatalf("a validator mismatch and a scan page both need a person: %+v open=%d", r, f.open("j1"))
	}
	ex := exceptions.Repository{}
	ok := func(exceptions.Row) exceptions.Context { return exceptions.Context{PartialEligible: true} }
	if err := f.do(func(tx store.Tx) error {
		if _, err := ex.Decide(tx, "d1", []string{"j1"}, []string{exceptions.ItemIDFor("j1", exceptions.ValidatorMismatch, "u1:0-4")}, exceptions.Redact, ok); err != nil {
			return err
		}
		_, err := ex.Decide(tx, "d2", []string{"j1"}, []string{exceptions.ItemIDFor("j1", exceptions.ScanPage, "part:page2")}, exceptions.Redact, ok)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, ds := f.stored("j1")
	if r.Dispositions != 2 || f.open("j1") != 0 {
		t.Fatalf("%+v %+v open=%d", r, ds, f.open("j1"))
	}
	var phone, page Disposition
	for _, d := range ds {
		if d.Category == "phone" {
			phone = d
		} else {
			page = d
		}
	}
	if phone.Action != Delete || phone.Basis != HumanItem || page.ID != "page2" || page.Action != RemovePart {
		t.Fatalf("%+v %+v", phone, page)
	}
	// decisions on exceptions move the resolution key: the earlier run is out of date
	if r2, _ := f.svc.Run(context.Background(), req("j1")); r2.Key != r.Key {
		t.Fatal("a repeated run after the decisions must be stable")
	}
	prior := r.Key
	// a decided exception is no longer drafted (it is resolved); changing one's mind is
	// revoke, resolve again (the exception reopens), then decide
	id := exceptions.ItemIDFor("j1", exceptions.ScanPage, "part:page2")
	if err = f.do(func(tx store.Tx) error { _, err := ex.Revoke(tx, "u1", []string{"j1"}, id); return err }); err != nil {
		t.Fatal(err)
	}
	r, err = f.svc.Run(context.Background(), req("j1"))
	if err != nil || r.Key == prior || r.Sync.Reopened != 1 || f.open("j1") != 1 {
		t.Fatalf("%+v %v open=%d", r, err, f.open("j1"))
	}
	if err = f.do(func(tx store.Tx) error {
		_, err := ex.Decide(tx, "d3", []string{"j1"}, []string{id}, exceptions.Keep, ok)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Run(context.Background(), req("j1")); err != nil {
		t.Fatal(err)
	}
	_, _, ds = f.stored("j1")
	kept := false
	for _, d := range ds {
		kept = kept || (d.ID == "page2" && d.Action == Keep)
	}
	if !kept {
		t.Fatalf("%+v", ds)
	}
}

func TestAGapKeptByDecisionIsStillAGapAndKeepsItsKeepDisposition(t *testing.T) {
	f := ssetup(t)
	f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1))
	kept := scanPart("page2")
	kept.Disposition = coverage.RetainedByPolicy
	f.ledger("j1", textPart("doc"), kept)
	if err := f.do(func(tx store.Tx) error {
		_, err := (exceptions.Repository{}).Sync(tx, "j1", []exceptions.Draft{{Type: exceptions.ScanPage, ScopeKey: "part:page2", Reason: "scan_page"}}, "seed")
		if err != nil {
			return err
		}
		_, err = (exceptions.Repository{}).Decide(tx, "d", []string{"j1"}, []string{exceptions.ItemIDFor("j1", exceptions.ScanPage, "part:page2")}, exceptions.Keep,
			func(exceptions.Row) exceptions.Context { return exceptions.Context{PartialEligible: true} })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(context.Background(), req("j1")); err != nil {
		t.Fatal(err)
	}
	_, _, ds := f.stored("j1")
	found := false
	for _, d := range ds {
		found = found || (d.ID == "page2" && d.Action == Keep && d.Basis == HumanItem)
	}
	if !found {
		t.Fatalf("a retained gap vanished from the resolution: %+v", ds)
	}
}

func TestAnIncompleteInventoryStopsTheStageInsteadOfReadingAGapList(t *testing.T) {
	f := ssetup(t)
	f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1))
	if _, err := f.svc.Run(context.Background(), req("j1")); !errors.Is(err, ErrCoverageIncomplete) {
		t.Fatalf("no ledger at all: %v", err)
	}
	if err := f.do(func(tx store.Tx) error {
		var r coverage.Repository
		if err := r.Begin(tx, "j2", "a"); err != nil {
			return err
		}
		return r.Record(tx, "j2", "a", textPart("doc"))
	}); err != nil {
		t.Fatal(err)
	}
	f.detect("j2", nf("u1", 0, "phone", kernel.SourceRule, 1))
	if _, err := f.svc.Run(context.Background(), req("j2")); !errors.Is(err, ErrCoverageIncomplete) {
		t.Fatalf("an unfinished enumeration: %v", err)
	}
	if _, ok, _ := f.stored("j1"); ok {
		t.Fatal("a failed run stored a resolution")
	}
	if f.open("j1") != 0 {
		t.Fatal("a failed run stored exceptions")
	}
}

func TestADecisionCommittedWhileComputingForcesARecomputeAndNothingStaleIsStored(t *testing.T) {
	f := ssetup(t)
	ids := f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1))
	f.ledger("j1", textPart("doc"))
	moved := false
	start := f.calls
	f.hook = func(n int) {
		if !moved && n >= start+5 { // after the reads, before the version-checked commit
			moved = true
			h := f.hook
			f.hook = nil
			if _, err := f.rev.DecideItem(context.Background(), review.ItemRequest{Request: "late", Batch: "b1", Job: "j1", Finding: ids[0], Keep: true}); err != nil {
				t.Error(err)
			}
			f.hook = h
		}
	}
	r, err := f.svc.Run(context.Background(), req("j1"))
	f.hook = nil
	if err != nil || !moved {
		t.Fatalf("moved=%v %v", moved, err)
	}
	if r.Attempts != 2 || r.DecisionVersion != 1 {
		t.Fatalf("the run must have been repeated against the new decision: %+v", r)
	}
	_, _, ds := f.stored("j1")
	if len(ds) != 1 || ds[0].Action != Keep {
		t.Fatalf("a stale resolution was stored: %+v", ds)
	}
	// decisions that keep changing exhaust the retries and store nothing
	f2 := ssetup(t)
	ids2 := f2.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1))
	f2.ledger("j1", textPart("doc"))
	n := 0
	f2.svc.Retries = 2
	f2.hook = func(int) {
		h := f2.hook
		f2.hook = nil
		n++
		if _, err := f2.rev.DecideItem(context.Background(), review.ItemRequest{Request: "x" + strconv.Itoa(n), Batch: "b1", Job: "j1", Finding: ids2[0], Keep: n%2 == 0}); err != nil {
			t.Error(err)
		}
		f2.hook = h
	}
	if _, err = f2.svc.Run(context.Background(), req("j1")); !errors.Is(err, ErrInputsChanged) {
		t.Fatalf("%v", err)
	}
	f2.hook = nil
	if _, ok, _ := f2.stored("j1"); ok || f2.open("j1") != 0 {
		t.Fatal("an abandoned run stored something")
	}
}

func TestSimulateWritesNothingAndOnlyCoversTheViewport(t *testing.T) {
	f := ssetup(t)
	ids := f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1), nf("u2", 0, "phone", kernel.SourceRule, 1), nf("u3", 0, "email", kernel.SourceRule, 1))
	f.ledger("j1", textPart("doc"), scanPart("p"))
	out, err := f.svc.Simulate(context.Background(), "j1", base(), []string{"u1", "u3"}, map[int64]Decision{ids[0]: {Keep: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Dispositions) != 2 || out.Dispositions[0].Unit != "u1" || out.Dispositions[0].Action != Keep || out.Dispositions[1].Unit != "u3" || out.Dispositions[1].Action != Mask {
		t.Fatalf("%+v", out.Dispositions)
	}
	if len(out.Drafts) != 0 {
		t.Fatalf("gaps are outside a viewport: %+v", out.Drafts)
	}
	if _, ok, _ := f.stored("j1"); ok || f.open("j1") != 0 {
		t.Fatal("a simulation wrote a resolution or an exception")
	}
	var v uint64
	f.do(func(tx store.Tx) (err error) { v, err = review.Version(tx, "j1"); return })
	if v != 0 {
		t.Fatal("a simulation changed the decision version")
	}
	// the draft decision did not leak into the next real run
	r, _ := f.svc.Run(context.Background(), req("j1"))
	_, _, ds := f.stored("j1")
	for _, d := range ds {
		if d.Unit == "u1" && d.Action != Delete {
			t.Fatalf("the draft decision persisted: %+v %+v", r, d)
		}
	}
	for name, call := range map[string]func() error{
		"no units": func() error { _, err := f.svc.Simulate(context.Background(), "j1", base(), nil, nil); return err },
		"too many": func() error {
			_, err := f.svc.Simulate(context.Background(), "j1", base(), make([]string, MaxViewport+1), nil)
			return err
		},
		"no job": func() error {
			_, err := f.svc.Simulate(context.Background(), "", base(), []string{"u1"}, nil)
			return err
		},
		"unwired": func() error {
			_, err := (&Service{}).Simulate(context.Background(), "j1", base(), []string{"u1"}, nil)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEnrichmentFeedsUnreliableLocationsAndPolicyKeeps(t *testing.T) {
	f := ssetup(t)
	ids := f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1), nf("u1", 20, "phone", kernel.SourceRule, 1))
	f.ledger("j1", textPart("doc"))
	f.svc.Enrich = func(x findings.Finding) Enrichment {
		return Enrichment{Unreliable: x.ID == ids[0], KeepByPolicy: x.ID == ids[1]}
	}
	r, err := f.svc.Run(context.Background(), req("j1"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, ds := f.stored("j1")
	if r.Drafts != 1 || len(ds) != 1 || ds[0].Action != Keep || ds[0].Basis != AutoPolicy {
		t.Fatalf("%+v %+v", r, ds)
	}
}

func TestPurgeAndPagingBounds(t *testing.T) {
	f := ssetup(t)
	f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1), nf("u1", 9, "phone", kernel.SourceRule, 1), nf("u1", 18, "phone", kernel.SourceRule, 1))
	f.detect("j2", nf("u1", 0, "phone", kernel.SourceRule, 1))
	f.ledger("j1", textPart("doc"))
	f.ledger("j2", textPart("doc"))
	f.svc.Run(context.Background(), req("j1"))
	f.svc.Run(context.Background(), req("j2"))
	var page []Disposition
	f.do(func(tx store.Tx) (err error) { page, err = Dispositions(tx, "j1", 1, 1); return })
	if len(page) != 1 || page[0].Start != 9 {
		t.Fatalf("keyset paging: %+v", page)
	}
	for name, c := range map[string][3]any{"no job": {"", 0, 5}, "negative": {"j1", -1, 5}, "zero limit": {"j1", 0, 0}, "big": {"j1", 0, MaxPage + 1}} {
		if err := f.do(func(tx store.Tx) error { _, err := Dispositions(tx, c[0].(string), c[1].(int), c[2].(int)); return err }); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := f.do(func(tx store.Tx) error { return Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	if _, ok, ds := f.stored("j1"); ok || len(ds) != 0 {
		t.Fatal("j1 survived")
	}
	if _, ok, ds := f.stored("j2"); !ok || len(ds) != 1 {
		t.Fatal("j2 lost its resolution")
	}
	if err := f.do(func(tx store.Tx) error { return Purge(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := (&Service{}).Run(context.Background(), req("j1")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(context.Background(), Request{Job: "j1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a request without a dispose digest: %v", err)
	}
}

func TestADetectionResultSealedWhileComputingForcesARecompute(t *testing.T) {
	f := ssetup(t)
	f.detect("j1", nf("u1", 0, "phone", kernel.SourceRule, 1))
	f.ledger("j1", textPart("doc"))
	moved := false
	start := f.calls
	f.hook = func(n int) {
		if !moved && n >= start+5 {
			moved = true
			h := f.hook
			f.hook = nil
			f.detect("j1", nf("u1", 30, "phone", kernel.SourceRule, 1)) // a supplementary scan finds one more
			f.hook = h
		}
	}
	r, err := f.svc.Run(context.Background(), req("j1"))
	f.hook = nil
	if err != nil || !moved || r.Attempts != 2 || r.DetectVersion != 2 || r.Dispositions != 2 {
		t.Fatalf("moved=%v %+v %v", moved, r, err)
	}
	if _, _, ds := f.stored("j1"); len(ds) != 2 {
		t.Fatalf("the resolution lacks the late finding: %+v", ds)
	}
}

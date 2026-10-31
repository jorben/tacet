//go:build darwin || windows

package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/verdict"
	_ "github.com/ncruces/go-sqlite3/driver"
)

type fx struct {
	t     *testing.T
	db    *store.DB
	svc   *Service
	dir   string
	key   string // current disposition key per the fake job table
	owner bool   // does the attempt still own the job
	mu    sync.Mutex
}

func setup(t *testing.T) *fx {
	t.Helper()
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "p.db")))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.NewRegistry(Module())
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
	f := &fx{t: t, db: db, dir: t.TempDir(), key: "k1", owner: true}
	f.svc = &Service{
		Commit: func(ctx context.Context, fn func(store.Tx) error) error {
			f.mu.Lock() // the workspace commit section is serial
			defer f.mu.Unlock()
			tx, err := db.Begin(ctx)
			if err != nil {
				return err
			}
			if err = fn(tx); err != nil {
				tx.Rollback()
				return err
			}
			return tx.Commit()
		},
		Admit: func(store.Tx, kernel.AttemptRef) error {
			if !f.owner {
				return errors.New("stale")
			}
			return nil
		},
		CurrentKey: func(store.Tx, string) (string, error) { return f.key, nil },
	}
	return f
}

func digest(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

var ready = verdict.Facts{Phase: verdict.PhaseReady, Validated: true, InputVerified: true, PublishGranted: true}

func (f *fx) req(content, name string) Request {
	return Request{Attempt: kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: 1, JobVersion: 1}, Key: "k1", Facts: ready,
		Candidate: strings.NewReader(content), Digest: digest([]byte(content)), Dir: f.dir, Name: name}
}

func (f *fx) grant(partial bool) {
	f.t.Helper()
	if err := f.svc.Grant(context.Background(), "j1", "k1", Explicit, partial); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) files() []string {
	es, _ := os.ReadDir(f.dir)
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

func (f *fx) query(fn func(*store.Txn) error) {
	f.t.Helper()
	if err := f.svc.Commit(context.Background(), func(tx store.Tx) error {
		t, _ := store.InTransaction(tx)
		return fn(t)
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) intentStates() map[string]int {
	out := map[string]int{}
	f.query(func(t *store.Txn) error {
		rs, err := t.Query("SELECT state, COUNT(*) FROM publish_intent GROUP BY state")
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var s string
			var n int
			rs.Scan(&s, &n)
			out[s] = n
		}
		return nil
	})
	return out
}

func TestPublishWritesTheCandidateWithoutOverwritingAndRecordsTheDelivery(t *testing.T) {
	f := setup(t)
	f.grant(false)
	d, err := f.svc.Publish(context.Background(), f.req("redacted body", "out-001.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || !d.Atomic || d.Partial || d.Name != "out-001.docx" {
		t.Fatalf("%+v", d)
	}
	got, err := os.ReadFile(filepath.Join(f.dir, "out-001.docx"))
	if err != nil || string(got) != "redacted body" {
		t.Fatalf("%q %v", got, err)
	}
	if names := f.files(); len(names) != 1 {
		t.Fatalf("temporary files left behind: %v", names)
	}
	if st := f.intentStates(); st["published"] != 1 || len(st) != 1 {
		t.Fatalf("%v", st)
	}
	var ds []Delivery
	var sup []bool
	f.query(func(t *store.Txn) error { return nil })
	f.svc.Commit(context.Background(), func(tx store.Tx) (err error) { ds, sup, err = Deliveries(tx, "j1"); return })
	if len(ds) != 1 || ds[0].Digest != digest([]byte("redacted body")) || sup[0] {
		t.Fatalf("%+v %v", ds, sup)
	}
	if runtimeIsUnix() {
		if fi, _ := os.Stat(filepath.Join(f.dir, "out-001.docx")); fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("published file is readable by others: %v", fi.Mode())
		}
	}
}

func runtimeIsUnix() bool { return filepath.Separator == '/' }

func TestAnExistingTargetIsNeverReplacedAndTheNameIsReportedAsAConflict(t *testing.T) {
	f := setup(t)
	f.grant(false)
	os.WriteFile(filepath.Join(f.dir, "out.docx"), []byte("someone else's file"), 0o600)
	_, err := f.svc.Publish(context.Background(), f.req("candidate", "out.docx"))
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("%v", err)
	}
	got, _ := os.ReadFile(filepath.Join(f.dir, "out.docx"))
	if string(got) != "someone else's file" {
		t.Fatal("an existing file was overwritten")
	}
	if names := f.files(); len(names) != 1 {
		t.Fatalf("temporary file left: %v", names)
	}
	if st := f.intentStates(); st["conflict"] != 1 || len(st) != 1 {
		t.Fatalf("%v", st)
	}
}

func TestTheGateAuthorizationKeyOwnershipAndDigestAreAllEnforcedBeforeAnythingIsVisible(t *testing.T) {
	cases := map[string]struct {
		prep func(f *fx, r *Request)
		want error
	}{
		"not validated":         {func(f *fx, r *Request) { r.Facts.Validated = false }, ErrNotReady},
		"pending exception":     {func(f *fx, r *Request) { r.Facts.PendingExceptions = 1 }, ErrNotReady},
		"unverified input":      {func(f *fx, r *Request) { r.Facts.InputVerified = false }, ErrNotReady},
		"no grant":              {func(f *fx, r *Request) { f.svc.Revoke(context.Background(), "j1") }, ErrNoGrant},
		"grant for another key": {func(f *fx, r *Request) { f.svc.Grant(context.Background(), "j1", "old", Explicit, false) }, ErrNoGrant},
		"key moved on after the candidate was made": {func(f *fx, r *Request) { f.key = "k2" }, ErrStale},
		"attempt no longer owns the job":            {func(f *fx, r *Request) { f.owner = false }, ErrStale},
		"digest does not match":                     {func(f *fx, r *Request) { r.Digest = digest([]byte("other")) }, ErrDigest},
	}
	for name, c := range cases {
		f := setup(t)
		f.grant(false)
		r := f.req("candidate", "out.docx")
		c.prep(f, &r)
		if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if names := f.files(); len(names) != 0 {
			t.Errorf("%s: left %v", name, names)
		}
		if st := f.intentStates(); len(st) != 0 {
			t.Errorf("%s: intent recorded %v", name, st)
		}
	}
}

func TestAGrantIsBoundToTheDispositionKeyAndOnlyTheJobItBelongsTo(t *testing.T) {
	f := setup(t)
	f.grant(false)
	f.svc.Grant(context.Background(), "j2", "kx", Explicit, false)
	f.key = "k1"
	// j2's key does not match the request's; j1's grant is unaffected by j2's
	if _, err := f.svc.Publish(context.Background(), f.req("a", "a.docx")); err != nil {
		t.Fatal(err)
	}
	r := f.req("b", "b.docx")
	r.Attempt.JobID = "j2"
	if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("%v", err)
	}
}

func TestAPartialResultNeedsAPartialGrantAndARecognisableName(t *testing.T) {
	f := setup(t)
	r := f.req("c", "out.docx")
	r.Facts.RetainedGaps, r.Facts.PartialEligible = 1, true
	f.grant(false)
	if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unmarked name: %v", err)
	}
	r = f.req("c", "out"+PartialMarker+".docx")
	r.Facts.RetainedGaps, r.Facts.PartialEligible = 1, true
	if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("a grant that does not cover a partial result: %v", err)
	}
	f.grant(true)
	r = f.req("c", "out"+PartialMarker+".docx")
	r.Facts.RetainedGaps, r.Facts.PartialEligible = 1, true
	d, err := f.svc.Publish(context.Background(), r)
	if err != nil || !d.Partial {
		t.Fatalf("%+v %v", d, err)
	}
	r = f.req("c", "x"+PartialMarker+".docx")
	r.Facts.RetainedGaps = 1 // kept, but the partial result is not eligible
	if _, err = f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNotReady) {
		t.Fatalf("%v", err)
	}
}

func TestInvalidRequestsAreRefused(t *testing.T) {
	f := setup(t)
	f.grant(false)
	for name, edit := range map[string]func(*Request){
		"no candidate": func(r *Request) { r.Candidate = nil }, "relative dir": func(r *Request) { r.Dir = "rel" }, "missing dir": func(r *Request) { r.Dir = filepath.Join(f.dir, "nope") },
		"path in name": func(r *Request) { r.Name = "../x.docx" }, "no key": func(r *Request) { r.Key = "" }, "bad digest form": func(r *Request) { r.Digest = "md5:abc" },
		"no job": func(r *Request) { r.Attempt.JobID = "" },
	} {
		r := f.req("c", "ok.docx")
		edit(&r)
		if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(f.files()) != 0 {
		t.Fatalf("%v", f.files())
	}
	if _, err := (&Service{}).Publish(context.Background(), f.req("c", "ok.docx")); !errors.Is(err, ErrInvalid) {
		t.Fatal("unwired service")
	}
}

func TestValidNameRejectsWhatIsUnsafeOnEitherPlatform(t *testing.T) {
	for _, n := range []string{"out-001.docx", "文件 1.pdf", "a.b.c", "COMMON.txt", "a-partial.docx"} {
		if !ValidName(n) {
			t.Errorf("%q refused", n)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b", "a\x00b", "a\nb", "trail.", "trail ", "CON", "con.txt", "NUL.docx", "lpt1", "Com9.x",
		".tacet-abc.tmp", strings.Repeat("a", 201), "bad\xff.docx"} {
		if ValidName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestWithoutAtomicRenameThePublishNeedsConsentAndIsRecordedAsNonAtomic(t *testing.T) {
	f := setup(t)
	f.grant(false)
	f.svc.rename = func(string, string) error { return platform.ErrPublishUnavailable }
	if _, err := f.svc.Publish(context.Background(), f.req("c", "out.docx")); !errors.Is(err, ErrAtomicUnavailable) {
		t.Fatalf("%v", err)
	}
	if len(f.files()) != 0 {
		t.Fatalf("a refused fallback left %v", f.files())
	}
	f.grant(false)
	r := f.req("c", "out.docx")
	r.AllowNonAtomic = true
	d, err := f.svc.Publish(context.Background(), r)
	if err != nil || d.Atomic {
		t.Fatalf("%+v %v", d, err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.dir, "out.docx")); string(got) != "c" || len(f.files()) != 1 {
		t.Fatalf("%q %v", got, f.files())
	}
	// the fallback also refuses to replace
	os.WriteFile(filepath.Join(f.dir, "taken.docx"), []byte("mine"), 0o600)
	r = f.req("c", "taken.docx")
	r.AllowNonAtomic = true
	if _, err = f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("%v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.dir, "taken.docx")); string(got) != "mine" {
		t.Fatal("the fallback overwrote")
	}
}

func TestAnIOFailureOfTheRenameAbortsAndCleansUp(t *testing.T) {
	f := setup(t)
	f.grant(false)
	f.svc.rename = func(string, string) error { return platform.ErrPublishIO }
	if _, err := f.svc.Publish(context.Background(), f.req("c", "out.docx")); !errors.Is(err, ErrIO) {
		t.Fatalf("%v", err)
	}
	if len(f.files()) != 0 || f.intentStates()["aborted"] != 1 {
		t.Fatalf("%v %v", f.files(), f.intentStates())
	}
}

func TestANewDeliverySupersedesTheOldOneAndKeepsIt(t *testing.T) {
	f := setup(t)
	f.grant(false)
	if _, err := f.svc.Publish(context.Background(), f.req("one", "v1.docx")); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.Publish(context.Background(), f.req("two", "v2.docx"))
	if err != nil || d.Version != 2 {
		t.Fatalf("%+v %v", d, err)
	}
	var ds []Delivery
	var sup []bool
	f.svc.Commit(context.Background(), func(tx store.Tx) (err error) { ds, sup, err = Deliveries(tx, "j1"); return })
	if len(ds) != 2 || ds[0].Version != 2 || sup[0] || !sup[1] {
		t.Fatalf("%+v %v", ds, sup)
	}
	for _, n := range []string{"v1.docx", "v2.docx"} {
		if _, err := os.Stat(filepath.Join(f.dir, n)); err != nil {
			t.Fatalf("a superseded delivery file was removed: %v", err)
		}
	}
}

func TestConcurrentPublishersOfTheSameNameHaveExactlyOneWinner(t *testing.T) {
	f := setup(t)
	f.grant(false)
	var wg sync.WaitGroup
	res := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.svc.Publish(context.Background(), f.req(strings.Repeat("x", i+1), "same.docx"))
			res <- err
		}(i)
	}
	wg.Wait()
	close(res)
	wins := 0
	for err := range res {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrNameConflict):
			t.Errorf("%v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
	if names := f.files(); len(names) != 1 {
		t.Fatalf("%v", names)
	}
}

// crashAfterIntent leaves exactly what a crash between the durable intent and
// the rename leaves: a prepared intent and a temporary file.
func (f *fx) crashAfterIntent(content, name string) (temp string) {
	f.t.Helper()
	f.svc.rename = func(string, string) error { panic("crash") }
	func() {
		defer func() { recover() }()
		f.svc.Publish(context.Background(), f.req(content, name))
	}()
	f.svc.rename = nil
	for _, n := range f.files() {
		if strings.HasPrefix(n, tempPrefix) {
			return n
		}
	}
	f.t.Fatal("no temporary file after the simulated crash")
	return ""
}

func TestRecoveryCompletesAPublishWhoseRenameLandedButWhoseRecordDidNot(t *testing.T) {
	f := setup(t)
	f.grant(false)
	temp := f.crashAfterIntent("final bytes", "out.docx")
	if f.intentStates()["prepared"] != 1 {
		t.Fatalf("%v", f.intentStates())
	}
	if err := os.Rename(filepath.Join(f.dir, temp), filepath.Join(f.dir, "out.docx")); err != nil { // the rename had happened
		t.Fatal(err)
	}
	rec, err := f.svc.Recover(context.Background())
	if err != nil || len(rec) != 1 || rec[0].Outcome != "published" || rec[0].Job != "j1" {
		t.Fatalf("%+v %v", rec, err)
	}
	var ds []Delivery
	f.svc.Commit(context.Background(), func(tx store.Tx) (err error) { ds, _, err = Deliveries(tx, "j1"); return })
	if len(ds) != 1 || ds[0].Name != "out.docx" {
		t.Fatalf("%+v", ds)
	}
	if again, _ := f.svc.Recover(context.Background()); len(again) != 0 {
		t.Fatalf("recovery is not idempotent: %+v", again)
	}
}

func TestRecoveryNeverOverwritesOrRepublishesAFileWithOtherContent(t *testing.T) {
	f := setup(t)
	f.grant(false)
	temp := f.crashAfterIntent("final bytes", "out.docx")
	os.WriteFile(filepath.Join(f.dir, "out.docx"), []byte("a different file now"), 0o600)
	rec, err := f.svc.Recover(context.Background())
	if err != nil || len(rec) != 1 || rec[0].Outcome != "conflict" {
		t.Fatalf("%+v %v", rec, err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.dir, "out.docx")); string(got) != "a different file now" {
		t.Fatal("recovery touched the target")
	}
	if _, err = os.Stat(filepath.Join(f.dir, temp)); err == nil {
		t.Fatal("the leftover temporary file survived")
	}
	var n int
	f.svc.Commit(context.Background(), func(tx store.Tx) error {
		tt, _ := store.InTransaction(tx)
		return tt.QueryRow("SELECT COUNT(*) FROM publish_delivery").Scan(&n)
	})
	if n != 0 {
		t.Fatal("a conflicting file was recorded as a delivery")
	}
}

func TestRecoveryRemovesTheTemporaryFileWhenNothingWasPublishedAndAllowsAnotherTry(t *testing.T) {
	f := setup(t)
	f.grant(false)
	temp := f.crashAfterIntent("final bytes", "out.docx")
	rec, err := f.svc.Recover(context.Background())
	if err != nil || len(rec) != 1 || rec[0].Outcome != "aborted" {
		t.Fatalf("%+v %v", rec, err)
	}
	if _, err = os.Stat(filepath.Join(f.dir, temp)); err == nil || len(f.files()) != 0 {
		t.Fatalf("%v", f.files())
	}
	d, err := f.svc.Publish(context.Background(), f.req("final bytes", "out.docx"))
	if err != nil || d.Version != 1 {
		t.Fatalf("a publish after an aborted intent: %+v %v", d, err)
	}
}

func TestRecoveryDoesNotDeleteAFileItDidNotCreate(t *testing.T) {
	f := setup(t)
	f.grant(false)
	f.crashAfterIntent("final bytes", "out.docx")
	// tamper: the intent now claims a temp name that is not ours
	f.svc.Commit(context.Background(), func(tx store.Tx) error {
		_, sc, _ := scope(tx)
		_, err := sc.Exec("UPDATE publish_intent SET temp='precious.docx'")
		return err
	})
	os.WriteFile(filepath.Join(f.dir, "precious.docx"), []byte("keep"), 0o600)
	f.svc.Recover(context.Background())
	if got, _ := os.ReadFile(filepath.Join(f.dir, "precious.docx")); string(got) != "keep" {
		t.Fatal("recovery deleted a file that is not a Tacet temporary file")
	}
}

func TestArtifactsAreWrittenOnceAndReplayIsHarmless(t *testing.T) {
	f := setup(t)
	a := Artifacts{Dir: f.dir}
	ctx := context.Background()
	if err := a.WriteArtifact(ctx, "report-1.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteArtifact(ctx, "report-1.json", []byte(`{"a":1}`)); err != nil {
		t.Fatalf("replaying the same artifact: %v", err)
	}
	if err := a.WriteArtifact(ctx, "report-1.json", []byte(`{"a":2}`)); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("%v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.dir, "report-1.json")); !bytes.Equal(got, []byte(`{"a":1}`)) {
		t.Fatalf("overwritten: %q", got)
	}
	if len(f.files()) != 1 {
		t.Fatalf("%v", f.files())
	}
	for name, call := range map[string]func() error{
		"bad name": func() error { return a.WriteArtifact(ctx, "../x", []byte("x")) },
		"bad dir":  func() error { return Artifacts{Dir: "rel"}.WriteArtifact(ctx, "x.json", []byte("x")) },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.WriteArtifact(cctx, "late.json", []byte("x")); err == nil {
		t.Error("a cancelled write went ahead")
	}
}

func TestPurgeRemovesRecordsButNeverTheDeliveredFiles(t *testing.T) {
	f := setup(t)
	f.grant(false)
	f.svc.Publish(context.Background(), f.req("c", "out.docx"))
	if err := f.svc.Commit(context.Background(), func(tx store.Tx) error { return Purge(tx, "j1") }); err != nil {
		t.Fatal(err)
	}
	var ds []Delivery
	f.svc.Commit(context.Background(), func(tx store.Tx) (err error) { ds, _, err = Deliveries(tx, "j1"); return })
	if len(ds) != 0 || len(f.intentStates()) != 0 {
		t.Fatal("records survived")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "out.docx")); err != nil {
		t.Fatal("the user's delivered file was deleted")
	}
	if err := f.svc.Commit(context.Background(), func(tx store.Tx) error { return Purge(tx, "") }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// readerHook runs fn when the candidate is first read, i.e. after the pre-check of
// the grant and before the serial section.
type readerHook struct {
	io.Reader
	fn   func()
	done bool
}

func (r *readerHook) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.fn()
	}
	return r.Reader.Read(p)
}

func TestTheGrantIsCheckedAgainInsideTheSerialSection(t *testing.T) {
	// the grant is withdrawn, or narrowed to exclude partial results, while the candidate is being copied
	f := setup(t)
	f.grant(false)
	r := f.req("c", "gone.docx")
	r.Candidate = &readerHook{Reader: r.Candidate, fn: func() { f.svc.Revoke(context.Background(), "j1") }}
	if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("a grant withdrawn during the copy: %v", err)
	}
	if len(f.files()) != 0 || len(f.intentStates()) != 0 {
		t.Fatalf("%v %v", f.files(), f.intentStates())
	}
	f.grant(true)
	r = f.req("c", "p"+PartialMarker+".docx")
	r.Facts.RetainedGaps, r.Facts.PartialEligible = 1, true
	r.Candidate = &readerHook{Reader: r.Candidate, fn: func() { f.grant(false) }}
	if _, err := f.svc.Publish(context.Background(), r); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("a grant narrowed during the copy: %v", err)
	}
	if len(f.files()) != 0 {
		t.Fatalf("%v", f.files())
	}
}

func TestTheOutcomeIsRecordedEvenIfTheCallerGivesUpAfterTheRename(t *testing.T) {
	f := setup(t)
	f.grant(false)
	ctx, cancel := context.WithCancel(context.Background())
	f.svc.rename = func(a, b string) error {
		err := platform.PublishExclusive(a, b)
		cancel() // the caller loses patience exactly after the file is in place
		return err
	}
	d, err := f.svc.Publish(ctx, f.req("c", "out.docx"))
	if err != nil || d.Version != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	if st := f.intentStates(); st["published"] != 1 {
		t.Fatalf("the delivery was not recorded: %v", st)
	}
}

func TestAFileLargerThanTheLimitIsRefusedWithoutLeavingAnything(t *testing.T) {
	f := setup(t)
	f.grant(false)
	old := MaxBytes
	MaxBytes = 4
	defer func() { MaxBytes = old }()
	if _, err := f.svc.Publish(context.Background(), f.req("12345", "big.docx")); !errors.Is(err, ErrIO) {
		t.Fatalf("%v", err)
	}
	if len(f.files()) != 0 {
		t.Fatalf("%v", f.files())
	}
	if _, err := f.svc.Publish(context.Background(), f.req("1234", "ok.docx")); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	if err := (Artifacts{Dir: f.dir}).WriteArtifact(context.Background(), "a.json", []byte("12345")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an oversized artifact: %v", err)
	}
}

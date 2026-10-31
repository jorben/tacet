//go:build darwin || windows

package retention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/refs"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/workspace"
)

type memUser struct{ key []byte }

func (u *memUser) Load() ([]byte, error) {
	if u.key == nil {
		return nil, keys.ErrKeyNotFound
	}
	return append([]byte(nil), u.key...), nil
}
func (u *memUser) Save(k []byte) error { u.key = append([]byte(nil), k...); return nil }
func (u *memUser) Delete() error       { u.key = nil; return nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	w      *workspace.Workspace
	refs   *refs.Manager
	resDir string
	clock  *clock
	unpins []string
	erased int
	busy   map[string]bool
	jobs   []string
	svc    *Service
}

func tctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func setup(t *testing.T, extra ...Registration) *env {
	t.Helper()
	reg, err := store.NewRegistry(Module(), refs.Module(), store.Module{Name: "demo", Prefix: "demo_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		"CREATE TABLE demo_rows(job TEXT, v TEXT)"}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w, err := workspace.OpenManaged(tctx(t), root, workspace.Managed{UserKey: &memUser{}, Registry: reg, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	res := filepath.Join(root, "res")
	os.MkdirAll(res, 0o700)
	m, err := refs.New(res, func(kernel.AttemptRef) error { return nil },
		func(job string) ([]byte, error) { return w.Keys().Get(keys.Task, job) })
	if err != nil {
		t.Fatal(err)
	}
	m.UseCatalog(refs.NewSQLCatalog(w.Commit))
	e := &env{w: w, refs: m, resDir: res, clock: &clock{t: time.Unix(1_800_000_000, 0)}, busy: map[string]bool{}}
	cfg := Config{
		Commit: w.Commit,
		EraseKey: func(kind keys.Kind, id string) error {
			e.erased++
			return w.Keys().CryptoErase(kind, id)
		},
		Unpin:       func(tx store.Tx, ws, owner string) error { e.unpins = append(e.unpins, ws+"/"+owner); return nil },
		Workspace:   "ws1",
		PublishBusy: func(job string) bool { return e.busy[job] },
		Jobs:        func(context.Context) ([]string, error) { return e.jobs, nil },
		Now:         e.clock.now,
	}
	regs := append([]Registration{{Name: "refs", Func: func(ctx context.Context, job string) error {
		_, err := m.Purge(ctx, job)
		return err
	}}}, extra...)
	e.svc, err = New(cfg, regs...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// job creates a task key and one finished resource file for it.
func (e *env) job(t *testing.T, id string) string {
	t.Helper()
	if _, err := e.w.Keys().Create(keys.Task, id); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.resDir, "in-"+id)
	if err := os.WriteFile(path, []byte("synthetic "+id), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := e.refs.Adopt(id, "res-"+id, path); err != nil {
		t.Fatal(err)
	}
	e.jobs = append(e.jobs, id)
	return path
}

func rows(t *testing.T, e *env, query string, args ...any) (n int) {
	t.Helper()
	if err := e.w.Commit(tctx(t), func(tx store.Tx) error {
		c, err := store.InTransaction(tx)
		if err != nil {
			return err
		}
		return c.QueryRow(query, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGrantDeadlineRunsFromTheLastExplicitSaveAndNothingElseRenewsIt(t *testing.T) {
	e := setup(t)
	ctx := tctx(t)
	if ok, err := e.svc.Usable(ctx, "j1"); ok || err != nil {
		t.Fatalf("no grant must not be usable: %v %v", ok, err)
	}
	if err := e.svc.Grant(ctx, "j1", 7); err != nil {
		t.Fatal(err)
	}
	e.clock.add(6*24*time.Hour + 23*time.Hour)
	if ok, _ := e.svc.Usable(ctx, "j1"); !ok {
		t.Fatal("expired too early")
	}
	// background activity (Usable calls, Sweep) must not extend it
	e.jobs = []string{"j1"}
	if _, err := e.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.add(2 * time.Hour)
	if ok, _ := e.svc.Usable(ctx, "j1"); ok {
		t.Fatal("still usable past the deadline: something renewed it")
	}
	if err := e.svc.Grant(ctx, "j1", 7); err != nil { // explicit restore/save renews
		t.Fatal(err)
	}
	if ok, _ := e.svc.Usable(ctx, "j1"); !ok {
		t.Fatal("an explicit new grant must restore use")
	}
	for _, bad := range []int{0, -1, MaxDays + 1} {
		if err := e.svc.Grant(ctx, "j1", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("days %d accepted", bad)
		}
	}
	if err := e.svc.Grant(ctx, "", 7); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty owner accepted")
	}
}

func TestPurgeErasesTheKeyDeletesResourcesAndRowsAndDropsPins(t *testing.T) {
	var tx *txPurger
	e := setup(t)
	tx = &txPurger{}
	svc, err := New(e.svc.cfg, append(e.svc.purgers, Registration{Name: "demo", Tx: tx})...)
	if err != nil {
		t.Fatal(err)
	}
	path := e.job(t, "j1")
	other := e.job(t, "j2")
	e.w.Commit(tctx(t), func(tx store.Tx) error {
		c, _ := store.InTransaction(tx)
		sc, _ := c.Module("demo")
		sc.Exec("INSERT INTO demo_rows VALUES('j1','a'),('j2','b')")
		return nil
	})
	if err = svc.Grant(tctx(t), "j1", 7); err != nil {
		t.Fatal(err)
	}
	if err = svc.Purge(tctx(t), "j1"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.w.Keys().Get(keys.Task, "j1"); !errors.Is(err, keys.ErrKeyNotFound) {
		t.Fatalf("task key survived: %v", err)
	}
	if _, err = os.Stat(path); err == nil {
		t.Fatal("resource file survived")
	}
	if rows(t, e, "SELECT count(*) FROM refs_resource WHERE job='j1'") != 0 || rows(t, e, "SELECT count(*) FROM demo_rows WHERE job='j1'") != 0 {
		t.Fatal("rows survived")
	}
	if rows(t, e, "SELECT count(*) FROM retention_grant") != 0 || rows(t, e, "SELECT count(*) FROM retention_step") != 0 {
		t.Fatal("bookkeeping rows left after a complete purge")
	}
	if len(e.unpins) != 1 || e.unpins[0] != "ws1/j1" {
		t.Fatalf("pins: %v", e.unpins)
	}
	if _, err = e.w.Keys().Get(keys.Task, "j2"); err != nil {
		t.Fatalf("another job's key was touched: %v", err)
	}
	if _, err = os.Stat(other); err != nil || rows(t, e, "SELECT count(*) FROM demo_rows WHERE job='j2'") != 1 {
		t.Fatal("another job's data was touched")
	}
}

type txPurger struct{ fail bool }

func (p *txPurger) Purge(ctx context.Context, tx store.Tx, job string) error {
	c, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	sc, err := c.Module("demo")
	if err != nil {
		return err
	}
	if _, err = sc.Exec("DELETE FROM demo_rows WHERE job=?", job); err != nil {
		return err
	}
	if p.fail {
		return errors.New("injected failure after the delete")
	}
	return nil
}

func TestAFailedStepStaysVisibleIsRetriedAndDoneStepsAreNotRepeated(t *testing.T) {
	flaky := &flakyFiles{fails: 1}
	e := setup(t, Registration{Name: "flaky", Func: flaky.purge})
	e.job(t, "j1")
	e.svc.Grant(tctx(t), "j1", 7)
	if err := e.svc.Purge(tctx(t), "j1"); !errors.Is(err, ErrPurgeFailed) {
		t.Fatalf("first purge: %v", err)
	}
	steps, _ := e.svc.Steps(tctx(t), "j1")
	if steps["erase_key"] != "done" || steps["purge:refs"] != "done" || steps["purge:flaky"] != "failed" || steps["unpin"] != "done" {
		t.Fatalf("steps: %v", steps)
	}
	if rows(t, e, "SELECT count(*) FROM retention_grant") != 1 {
		t.Fatal("a half-done purge must not forget the job")
	}
	if err := e.svc.Purge(tctx(t), "j1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if e.erased != 1 || len(e.unpins) != 1 || flaky.calls != 2 {
		t.Fatalf("done steps repeated: erased=%d unpins=%d flaky=%d", e.erased, len(e.unpins), flaky.calls)
	}
	if rows(t, e, "SELECT count(*) FROM retention_step") != 0 {
		t.Fatal("step rows left after success")
	}
}

type flakyFiles struct{ fails, calls int }

func (f *flakyFiles) purge(context.Context, string) error {
	f.calls++
	if f.calls <= f.fails {
		return errors.New("disk busy")
	}
	return nil
}

func TestKeyErasureFailureStopsEverythingElse(t *testing.T) {
	e := setup(t)
	path := e.job(t, "j1")
	cfg := e.svc.cfg
	cfg.EraseKey = func(keys.Kind, string) error { return errors.New("keystore locked") }
	svc, _ := New(cfg, e.svc.purgers...)
	if err := svc.Purge(tctx(t), "j1"); !errors.Is(err, ErrPurgeFailed) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("data was deleted while its key could still decrypt it")
	}
	if steps, _ := svc.Steps(tctx(t), "j1"); steps["erase_key"] != "failed" || len(steps) != 1 {
		t.Fatalf("steps: %v", steps)
	}
}

func TestPurgeWaitsForAPublishInProgress(t *testing.T) {
	e := setup(t)
	path := e.job(t, "j1")
	e.busy["j1"] = true
	if err := e.svc.Purge(tctx(t), "j1"); !errors.Is(err, ErrPublishing) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.w.Keys().Get(keys.Task, "j1"); err != nil {
		t.Fatal("key erased during a publish")
	}
	if _, err := os.Stat(path); err != nil || e.erased != 0 {
		t.Fatal("something was removed during a publish")
	}
	e.busy["j1"] = false
	if err := e.svc.Purge(tctx(t), "j1"); err != nil {
		t.Fatal(err)
	}
}

func TestATransactionalPurgerRollsBackOnFailureAndIsRetried(t *testing.T) {
	p := &txPurger{fail: true}
	e := setup(t, Registration{Name: "demo", Tx: p})
	e.job(t, "j1")
	e.w.Commit(tctx(t), func(tx store.Tx) error {
		c, _ := store.InTransaction(tx)
		sc, _ := c.Module("demo")
		sc.Exec("INSERT INTO demo_rows VALUES('j1','a')")
		return nil
	})
	if err := e.svc.Purge(tctx(t), "j1"); !errors.Is(err, ErrPurgeFailed) {
		t.Fatalf("got %v", err)
	}
	if rows(t, e, "SELECT count(*) FROM demo_rows WHERE job='j1'") != 1 {
		t.Fatal("a failing transactional purger left a half-applied delete")
	}
	p.fail = false
	if err := e.svc.Purge(tctx(t), "j1"); err != nil {
		t.Fatal(err)
	}
	if rows(t, e, "SELECT count(*) FROM demo_rows WHERE job='j1'") != 0 {
		t.Fatal("retry did not delete")
	}
}

func TestSweepPurgesUngrantedAndExpiredJobsKeepsValidOnesAndResumesFailedPurges(t *testing.T) {
	flaky := &flakyFiles{fails: 1}
	e := setup(t, Registration{Name: "flaky", Func: func(ctx context.Context, job string) error {
		if job == "resume" {
			return flaky.purge(ctx, job)
		}
		return nil
	}})
	for _, j := range []string{"none", "valid", "old", "resume"} {
		e.job(t, j)
	}
	ctx := tctx(t)
	e.svc.Grant(ctx, "old", 1)
	e.svc.Grant(ctx, "resume", 30)
	e.svc.Grant(ctx, "valid", 7)
	if err := e.svc.Purge(ctx, "resume"); !errors.Is(err, ErrPurgeFailed) { // crashed mid-purge earlier
		t.Fatalf("setup: %v", err)
	}
	e.clock.add(2 * 24 * time.Hour)
	e.svc.Grant(ctx, "valid", 7)
	e.busy["none"] = true
	rep, err := e.svc.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kept != 1 || len(rep.Busy) != 1 || rep.Busy[0] != "none" {
		t.Fatalf("report: %+v", rep)
	}
	if len(rep.Purged) != 2 || len(rep.Expired) != 1 || rep.Expired[0] != "old" || len(rep.Failed) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if _, err := e.w.Keys().Get(keys.Task, "valid"); err != nil {
		t.Fatal("a job within its retention period was purged")
	}
	for _, j := range []string{"old", "resume"} {
		if _, err := e.w.Keys().Get(keys.Task, j); !errors.Is(err, keys.ErrKeyNotFound) {
			t.Fatalf("%s key survived", j)
		}
	}
	e.jobs = []string{"valid", "none"} // Jobs lists jobs that still hold data; purged ones are gone
	e.busy["none"] = false
	if rep, err = e.svc.Sweep(ctx); err != nil || len(rep.Purged) != 1 || rep.Purged[0] != "none" {
		t.Fatalf("second sweep: %+v %v", rep, err)
	}
}

func TestExpiredGrantIsMarkedBeforeThePurgeSoUseStaysBlockedEvenIfItFails(t *testing.T) {
	flaky := &flakyFiles{fails: 100}
	e := setup(t, Registration{Name: "flaky", Func: flaky.purge})
	e.job(t, "j1")
	ctx := tctx(t)
	e.svc.Grant(ctx, "j1", 1)
	e.clock.add(25 * time.Hour)
	rep, err := e.svc.Sweep(ctx)
	if err != nil || len(rep.Failed) != 1 {
		t.Fatalf("sweep: %+v %v", rep, err)
	}
	e.clock.add(-25 * time.Hour) // even a clock that goes backwards cannot revive an expired grant
	if ok, _ := e.svc.Usable(ctx, "j1"); ok {
		t.Fatal("an expired job became usable again")
	}
}

func TestNewValidatesItsInputs(t *testing.T) {
	ok := func(kind keys.Kind, id string) error { return nil }
	unpin := func(store.Tx, string, string) error { return nil }
	commit := func(context.Context, func(store.Tx) error) error { return nil }
	good := Config{Commit: commit, EraseKey: ok, Unpin: unpin}
	f := func(context.Context, string) error { return nil }
	for name, tc := range map[string]struct {
		cfg  Config
		regs []Registration
	}{
		"no commit":    {Config{EraseKey: ok, Unpin: unpin}, nil},
		"no erase":     {Config{Commit: commit, Unpin: unpin}, nil},
		"no unpin":     {Config{Commit: commit, EraseKey: ok}, nil},
		"unnamed":      {good, []Registration{{Func: f}}},
		"both kinds":   {good, []Registration{{Name: "a", Func: f, Tx: &txPurger{}}}},
		"neither kind": {good, []Registration{{Name: "a"}}},
		"duplicate":    {good, []Registration{{Name: "a", Func: f}, {Name: "a", Func: f}}},
	} {
		if _, err := New(tc.cfg, tc.regs...); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := New(good, Registration{Name: "a", Func: f}); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{cfg: good}).Purge(context.Background(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty job accepted")
	}
}

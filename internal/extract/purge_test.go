//go:build darwin || windows

package extract

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/coverage"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/retention"
	"github.com/jorben/tacet/internal/store"
)

// taskKeys stands in for the key manager: one task key per job.
func taskKeys() (derive func(job, id string) ([]byte, error), erase func(job string)) {
	taskKey := map[string][]byte{}
	derive = func(job, id string) ([]byte, error) {
		tk, ok := taskKey[job]
		if !ok {
			if job == "gone" {
				return nil, ErrBody
			}
			tk = bytes.Repeat([]byte(job[:1]), 32)
			taskKey[job] = tk
		}
		if tk == nil {
			return nil, ErrBody // erased
		}
		return keys.ResourceKey(tk, id)
	}
	erase = func(job string) { taskKey[job] = nil }
	return
}

func countRows(e *env, table string) int {
	return query(e, func(tx store.Tx) (int, error) {
		t, err := store.InTransaction(tx)
		if err != nil {
			return 0, err
		}
		var n int
		err = t.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		return n, err
	})
}

func TestRetentionPurgeRemovesRowsFindingsLedgerAndBodiesAndErasedKeysUnlockNothing(t *testing.T) {
	e := setup(t, nil)
	root := t.TempDir()
	derive, erase := taskKeys()
	bodies, err := JobBodies(root, "j1", derive)
	if err != nil {
		t.Fatal(err)
	}
	e.p.Bodies = bodies
	if _, err = e.run(e.plugin(normal), rulesOnly()); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "j1", "*.body"))
	if len(files) != 2 {
		t.Fatalf("bodies on disk: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	if bytes.Contains(raw, []byte("13812345678")) || bytes.Contains(raw, []byte("无敏感")) {
		t.Fatal("a body file holds plaintext")
	}
	units := query(e, func(tx store.Tx) ([]Unit, error) { return e.p.Units.Units(tx, "a1", 0, 10) })
	if text, err := e.p.Bodies.Get(units[0].Body); err != nil || text == "" {
		t.Fatalf("body unreadable before the purge: %v", err)
	}
	// another job's bodies must survive j1's purge
	other, _ := JobBodies(root, "k2", derive)
	if err = other.Put("b_other_1", "kept"); err != nil {
		t.Fatal(err)
	}

	erased := 0
	svc, err := retention.New(retention.Config{
		Commit:   e.ws.Commit,
		EraseKey: func(keys.Kind, string) error { erased++; erase("j1"); return nil },
		Unpin:    func(store.Tx, string, string) error { return nil },
		Jobs:     func(context.Context) ([]string, error) { return []string{"j1"}, nil },
	},
		retention.Registration{Name: "extract", Tx: retention.PurgerFunc(func(_ context.Context, tx store.Tx, job string) error { return e.p.Units.Purge(tx, job) })},
		retention.Registration{Name: "extract-bodies", Func: PurgeBodies(root)},
		retention.Registration{Name: "findings", Tx: retention.PurgerFunc(func(_ context.Context, tx store.Tx, job string) error { return e.p.Findings.Purge(tx, job) })},
		retention.Registration{Name: "coverage", Tx: retention.PurgerFunc(func(_ context.Context, tx store.Tx, job string) error { return coverage.Repository{}.Purge(tx, job) })},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Purge(context.Background(), "j1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"extract_unit", "extract_attempt", "findings_finding", "findings_attempt", "findings_result", "coverage_part", "coverage_inventory"} {
		if n := countRows(e, table); n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	if _, err = os.Stat(filepath.Join(root, "j1")); !os.IsNotExist(err) {
		t.Errorf("the job's body directory survived: %v", err)
	}
	if text, err := other.Get("b_other_1"); err != nil || text != "kept" {
		t.Errorf("another job's body was touched: %q %v", text, err)
	}
	if erased != 1 {
		t.Errorf("task key erased %d times", erased)
	}
	// the key is gone: even a restored copy of the file cannot be read
	if _, err = e.p.Bodies.Get(units[0].Body); err == nil {
		t.Error("a body is still readable after the task key was erased")
	}
}

func TestJobBodiesRefuseUnsafeJobsAndMissingKeys(t *testing.T) {
	derive, _ := taskKeys()
	root := t.TempDir()
	for _, job := range []string{"", "../x", "a/b", "a b", `a\b`} {
		if _, err := JobBodies(root, job, derive); err == nil {
			t.Errorf("job %q accepted", job)
		}
		if err := PurgeBodies(root)(context.Background(), job); err == nil {
			t.Errorf("purge of job %q accepted", job)
		}
	}
	if _, err := JobBodies(root, "j1", nil); err == nil {
		t.Error("no key derivation accepted")
	}
	if _, err := JobBodies("relative", "j1", derive); err == nil {
		t.Error("relative root accepted")
	}
	b, _ := JobBodies(root, "gone", derive)
	if err := b.Put("b1", "text"); err == nil {
		t.Error("a body was written without a key")
	}
	if _, err := os.Stat(filepath.Join(root, "gone", "b1.body")); err == nil {
		t.Error("a file was left behind by a failed write")
	}
	if err := PurgeBodies(root)(context.Background(), "never-existed"); err != nil {
		t.Errorf("purge of an absent job must be idempotent: %v", err)
	}
}

func TestPurgeRefusesAnEmptyJobAndBodyDirectoriesArePrivate(t *testing.T) {
	e := setup(t, nil)
	err := e.ws.Commit(context.Background(), func(tx store.Tx) error { return e.p.Units.Purge(tx, "") })
	if err == nil {
		t.Error("an empty job id purged something")
	}
	derive, _ := taskKeys()
	root := t.TempDir()
	b, _ := JobBodies(root, "j1", derive)
	if err := b.Put("b1", "text"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "j1"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("job body directory: %v %v", info.Mode(), err)
	}
}

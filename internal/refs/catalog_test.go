//go:build darwin || windows

package refs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/keys"
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

func tctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// start opens the real encrypted workspace in root and a manager on root/res.
func start(t *testing.T, root string, user keys.UserKeyStore) (*workspace.Workspace, *Manager, string) {
	t.Helper()
	reg, err := store.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	w, err := workspace.OpenManaged(tctx(t), root, workspace.Managed{UserKey: user, Registry: reg, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	res := filepath.Join(root, "res")
	if err = os.MkdirAll(res, 0o700); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdmit{gen: 4, retired: map[string]bool{}}
	m, err := New(res, fa.admit, taskKey)
	if err != nil {
		t.Fatal(err)
	}
	m.UseCatalog(NewSQLCatalog(w.Commit))
	return w, m, res
}

func finish(t *testing.T, m *Manager, job, attempt, payload string) (string, string) {
	t.Helper()
	g, err := m.IssueWrite(att(job, attempt), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files, wires, err := Handoff(g)
	if err != nil {
		t.Fatal(err)
	}
	writeCiphertext(t, g, files, wires[0], payload)
	d, err := g.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	return g.ResourceID(), d
}

func TestFinishedResourcesSurviveARestartAndCrashLeftoversAreRemoved(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, res := start(t, root, user)
	id, digest := finish(t, m, "j1", "a1", "private payload one")
	input := filepath.Join(res, "input.enc")
	os.WriteFile(input, []byte("encrypted input copy"), 0o400)
	if err := m.Adopt("j1", "in1", input); err != nil {
		t.Fatal(err)
	}
	// a write in progress when the process dies, and a sealing temporary
	dead, err := m.IssueWrite(att("j1", "a9"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(res, ".frozen-crashed"), []byte("partial"), 0o600)
	if err = w.Close(); err != nil { // the "crash": nothing flushed by the manager
		t.Fatal(err)
	}

	w, m, res = start(t, root, user)
	defer w.Close()
	rec, err := m.Recover(tctx(t))
	if err != nil || rec.Loaded != 2 || rec.Removed != 2 || len(rec.Missing) != 0 {
		t.Fatalf("recover: %+v %v", rec, err)
	}
	if _, err = os.Stat(filepath.Join(res, dead.ResourceID())); err == nil {
		t.Fatal("the unfinished write survived")
	}
	if got, err := m.Digest(id); err != nil || got != digest {
		t.Fatalf("digest after restart: %q %v", got, err)
	}
	if err = m.Verify(id); err != nil || m.Verify("in1") != nil {
		t.Fatal("verify after restart")
	}
	g, err := m.IssueRead(att("j1", "a2"), id)
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	_, wires, _ := Handoff(g)
	if wires[0].Digest != digest {
		t.Fatal("the wire digest changed across a restart")
	}
	if _, err = m.IssueRead(att("j2", "b1"), id); !errors.Is(err, ErrForeign) {
		t.Fatalf("another job reads it after restart: %v", err)
	}
	if again, _ := m.Recover(tctx(t)); again.Loaded != 0 {
		t.Fatal("a second Recover must refuse")
	}
}

func TestRecoverReportsAMissingOrResizedFileAndNeverServesIt(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, res := start(t, root, user)
	gone, _ := finish(t, m, "j1", "a1", "one")
	short, _ := finish(t, m, "j1", "a2", "two two two")
	ok, _ := finish(t, m, "j1", "a3", "three")
	var paths = map[string]string{}
	for _, id := range []string{gone, short, ok} {
		paths[id] = m.res[id].path
	}
	w.Close()
	os.Remove(paths[gone])
	if err := os.Truncate(paths[short], 5); err != nil {
		os.Chmod(paths[short], 0o600)
		os.Truncate(paths[short], 5)
	}
	w, m, _ = start(t, root, user)
	defer w.Close()
	rec, err := m.Recover(tctx(t))
	if err != nil || rec.Loaded != 3 || len(rec.Missing) != 2 {
		t.Fatalf("recover: %+v %v", rec, err)
	}
	for _, id := range []string{gone, short} {
		if _, err = m.IssueRead(att("j1", "r"), id); err == nil {
			t.Fatalf("%s served although damaged", id)
		}
		if d, err := m.Digest(id); err == nil {
			t.Fatalf("%s: a damaged resource still reports digest %s", id, d)
		}
		if err = m.Verify(id); err == nil {
			t.Fatalf("%s verified although damaged", id)
		}
	}
	if _, err = m.IssueRead(att("j1", "r"), ok); err != nil {
		t.Fatalf("intact resource: %v", err)
	}
	_ = res
}

type failingCatalog struct{ Catalog }

func (failingCatalog) Put(context.Context, Entry) error { return errors.New("disk full") }

func TestACatalogFailureLeavesNoReadableOrLeftoverResource(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, res := start(t, root, user)
	defer w.Close()
	m.UseCatalog(failingCatalog{NewSQLCatalog(w.Commit)})
	g, err := m.IssueWrite(att("j1", "a1"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files, wires, _ := Handoff(g)
	writeCiphertext(t, g, files, wires[0], "payload")
	if _, err = g.Freeze(); !errors.Is(err, ErrCatalog) {
		t.Fatalf("freeze: %v", err)
	}
	if _, err = m.Digest(g.ResourceID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resource still known: %v", err)
	}
	if entries, _ := os.ReadDir(res); len(entries) != 0 {
		t.Fatalf("files left: %d", len(entries))
	}
}

func TestRevokeFromTheCommitSectionDoesNotDeadlockWithFreeze(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, _ := start(t, root, user)
	defer w.Close()
	for i := 0; i < 20; i++ {
		g, err := m.IssueWrite(att("j1", "a1"), 1<<20)
		if err != nil {
			break // revoked by an earlier round: the attempt is fenced
		}
		files, wires, _ := Handoff(g)
		writeCiphertext(t, g, files, wires[0], "payload")
		done := make(chan struct{}, 2)
		go func() { g.Freeze(); done <- struct{}{} }()
		go func() {
			w.Commit(tctx(t), func(store.Tx) error { m.Revoke(att("j1", "a1")); return nil })
			done <- struct{}{}
		}()
		for n := 0; n < 2; n++ {
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("deadlock between Freeze and a Revoke issued inside the commit section")
			}
		}
	}
}

func TestPurgeRemovesRowsAndFilesAndRefusesWhileWritesAreOpen(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, res := start(t, root, user)
	a, _ := finish(t, m, "j1", "a1", "one")
	b, _ := finish(t, m, "j1", "a2", "two")
	keep, _ := finish(t, m, "j2", "b1", "other job")
	open, err := m.IssueWrite(att("j1", "a3"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Purge(tctx(t), "j1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purge with an open write: %v", err)
	}
	m.Revoke(att("j1", "a3"))
	_ = open
	n, err := m.Purge(tctx(t), "j1")
	if err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
	for _, id := range []string{a, b} {
		if _, err = m.Digest(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s still known", id)
		}
	}
	if err = m.Verify(keep); err != nil {
		t.Fatalf("another job's resource touched: %v", err)
	}
	w.Close()
	w, m, res = start(t, root, user)
	defer w.Close()
	rec, err := m.Recover(tctx(t))
	if err != nil || rec.Loaded != 1 || rec.Removed != 0 || len(rec.Missing) != 0 {
		t.Fatalf("after purge and restart: %+v %v", rec, err)
	}
	if entries, _ := os.ReadDir(res); len(entries) != 1 {
		t.Fatalf("files: %d", len(entries))
	}
}

func TestJobQuotaCountsFinishedBytesAndOpenLimitsPerJob(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, _ := start(t, root, user)
	defer w.Close()
	m.SetJobQuota(1000)
	g1, err := m.IssueWrite(att("j1", "a1"), 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.IssueWrite(att("j1", "a2"), 600); !errors.Is(err, ErrQuota) {
		t.Fatalf("second reservation beyond the quota: %v", err)
	}
	if _, err = m.IssueWrite(att("j2", "b1"), 900); err != nil {
		t.Fatalf("another job is not charged: %v", err)
	}
	files, wires, _ := Handoff(g1)
	writeCiphertext(t, g1, files, wires[0], "tiny")
	if _, err = g1.Freeze(); err != nil {
		t.Fatal(err)
	}
	// the open reservation is replaced by the real (small) size
	if _, err = m.IssueWrite(att("j1", "a3"), 900); err != nil {
		t.Fatalf("finished size should free the unused reservation: %v", err)
	}
	if _, err = m.IssueWrite(att("j1", "a4"), 200); !errors.Is(err, ErrQuota) {
		t.Fatalf("sum of finished and open: %v", err)
	}
}

func TestFinishedBytesAloneCanExhaustTheQuota(t *testing.T) {
	root, user := t.TempDir(), &memUser{}
	w, m, _ := start(t, root, user)
	defer w.Close()
	id, _ := finish(t, m, "j1", "a1", "some finished content")
	size := m.res[id].size
	m.SetJobQuota(size + 50)
	if _, err := m.IssueWrite(att("j1", "a2"), 51); !errors.Is(err, ErrQuota) {
		t.Fatalf("finished bytes were not charged: %v", err)
	}
	if _, err := m.IssueWrite(att("j1", "a3"), 50); err != nil {
		t.Fatalf("exactly the remaining quota must fit: %v", err)
	}
}

package keys

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirWrappedRoundTripNeverReplacesAndRejectsUnsafeIds(t *testing.T) {
	d, err := NewDirWrapped(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Load(Task, "t1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err = d.Save(Task, "t1", []byte("blob-1")); err != nil {
		t.Fatal(err)
	}
	if err = d.Save(Task, "t1", []byte("blob-2")); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("second save: %v", err)
	}
	if got, _ := d.Load(Task, "t1"); !bytes.Equal(got, []byte("blob-1")) {
		t.Fatal("a refused save changed the blob")
	}
	if err = d.Save(Batch, "t1", []byte("other")); err != nil {
		t.Fatalf("kinds must not collide: %v", err)
	}
	for _, bad := range []string{"../x", "a/b", `a\b`, "", "a.b", "a b", "x\x00"} {
		if err = d.Save(Task, bad, []byte("x")); !errors.Is(err, ErrKeyInvalid) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
	if err = d.Delete(Task, "t1"); err != nil {
		t.Fatal(err)
	}
	if err = d.Delete(Task, "t1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	entries, _ := os.ReadDir(d.dir)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	if _, err = NewDirWrapped("relative"); !errors.Is(err, ErrKeystoreUnavailable) {
		t.Fatalf("relative dir: %v", err)
	}
}

func TestDirWrappedWorksUnderTheManagerAndSurvivesReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	user := &memUser{}
	w1, _ := NewDirWrapped(dir)
	m1, err := Open(user, w1, true)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := m1.Create(Workspace, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	task, err := m1.Create(Task, "t1")
	if err != nil {
		t.Fatal(err)
	}
	m1.Close()
	w2, _ := NewDirWrapped(dir)
	m2, err := Open(user, w2, false)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if got, err := m2.Get(Workspace, "workspace"); err != nil || !bytes.Equal(got, ws) {
		t.Fatalf("workspace key: %v", err)
	}
	if got, err := m2.Get(Task, "t1"); err != nil || !bytes.Equal(got, task) {
		t.Fatalf("task key: %v", err)
	}
}

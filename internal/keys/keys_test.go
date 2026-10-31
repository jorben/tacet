package keys

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/jorben/tacet/internal/store"
)

type memUser struct {
	key     []byte
	err     error
	saveErr error
	save    int
}

func (u *memUser) Load() ([]byte, error) {
	if u.err != nil {
		return nil, u.err
	}
	if u.key == nil {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), u.key...), nil
}
func (u *memUser) Save(k []byte) error {
	u.save++
	if u.saveErr != nil {
		return u.saveErr
	}
	u.key = append([]byte(nil), k...)
	return nil
}
func (u *memUser) Delete() error { u.key = nil; return nil }

type memWrapped struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newWrapped() *memWrapped { return &memWrapped{m: map[string][]byte{}} }
func (w *memWrapped) Load(k Kind, id string) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, ok := w.m[string(k)+"/"+id]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), b...), nil
}
func (w *memWrapped) Save(k Kind, id string, b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.m[string(k)+"/"+id] = append([]byte(nil), b...)
	return nil
}
func (w *memWrapped) Delete(k Kind, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.m[string(k)+"/"+id]; !ok {
		return ErrKeyNotFound
	}
	delete(w.m, string(k)+"/"+id)
	return nil
}

func manager(t *testing.T) (*Manager, *memUser, *memWrapped) {
	t.Helper()
	u, w := &memUser{}, newWrapped()
	m, err := Open(u, w, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Create(Workspace, "workspace"); err != nil {
		t.Fatal(err)
	}
	return m, u, w
}

func TestOpenCreatesOnlyWhenAllowedAndNeverFallsBack(t *testing.T) {
	u, w := &memUser{}, newWrapped()
	if _, err := Open(u, w, false); !errors.Is(err, ErrKeyNotFound) || u.save != 0 {
		t.Fatalf("CLI must not create a fresh key: err=%v saves=%d", err, u.save)
	}
	m, err := Open(u, w, true)
	if err != nil || u.save != 1 || len(u.key) != keyLen {
		t.Fatalf("create: %v", err)
	}
	m.Close()
	// Opening again reuses the same key and does not save a new one.
	if _, err = Open(u, w, true); err != nil || u.save != 1 {
		t.Fatalf("reopen: %v saves=%d", err, u.save)
	}
	for name, st := range map[string]*memUser{
		"locked keychain": {err: errors.New("locked")},
		"short key":       {key: []byte("short")},
	} {
		_, err := Open(st, w, true)
		want := ErrKeystoreUnavailable
		if name == "short key" {
			want = ErrKeyInvalid
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = Open(nil, w, true); !errors.Is(err, ErrKeystoreUnavailable) {
		t.Fatal("nil store")
	}
	// A key that could not be persisted is unavailable: using it would make
	// everything encrypted under it unreadable after a restart.
	failing := &memUser{saveErr: errors.New("keychain write denied")}
	if _, err = Open(failing, w, true); !errors.Is(err, ErrKeystoreUnavailable) {
		t.Fatalf("unsaved key was used: %v", err)
	}
}

func TestWrapRoundTripAndHierarchy(t *testing.T) {
	m, _, w := manager(t)
	batch, err := m.Create(Batch, "b1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(Task, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(batch, task) || len(task) != keyLen {
		t.Fatal("keys must be independent random values")
	}
	got, err := m.Get(Task, "t1")
	if err != nil || !bytes.Equal(got, task) {
		t.Fatalf("round trip: %v", err)
	}
	// Stored blobs hold no plaintext key.
	for name, blob := range w.m {
		if bytes.Contains(blob, task) || bytes.Contains(blob, batch) {
			t.Fatalf("%s stores a plaintext key", name)
		}
	}
	// Workspace and config keys are wrapped by the user key; task keys by the
	// workspace key, so a different user key cannot open any of them.
	m2 := &Manager{user: bytes.Repeat([]byte{9}, keyLen), wrapped: w}
	for _, c := range []struct {
		k  Kind
		id string
	}{{Workspace, "workspace"}, {Task, "t1"}, {Batch, "b1"}} {
		if _, err = m2.Get(c.k, c.id); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("%s opened under another user key: %v", c.k, err)
		}
	}
}

func TestBlobIsBoundToItsIdentityAndIntegrityProtected(t *testing.T) {
	m, _, w := manager(t)
	m.Create(Task, "t1")
	m.Create(Task, "t2")
	m.Create(Batch, "t1") // same id, other kind
	// Moving t1's blob to t2 (or to the batch layer) must not decrypt.
	blob, _ := w.Load(Task, "t1")
	w.Save(Task, "t2", blob)
	if _, err := m.Get(Task, "t2"); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("blob accepted under another task id: %v", err)
	}
	w.Save(Batch, "t1", blob)
	if _, err := m.Get(Batch, "t1"); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("blob accepted under another kind: %v", err)
	}
	for name, mut := range map[string]func([]byte) []byte{
		"flipped ciphertext": func(b []byte) []byte { b[len(b)-1] ^= 1; return b },
		"flipped nonce":      func(b []byte) []byte { b[2] ^= 1; return b },
		"unknown version":    func(b []byte) []byte { b[0] = 2; return b },
		"truncated":          func(b []byte) []byte { return b[:10] },
		"empty":              func(b []byte) []byte { return nil },
	} {
		w.Save(Task, "t1", mut(append([]byte(nil), blob...)))
		if _, err := m.Get(Task, "t1"); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestCreateNeverReplacesAndValidatesInput(t *testing.T) {
	m, _, _ := manager(t)
	first, _ := m.Create(Task, "t1")
	if _, err := m.Create(Task, "t1"); !errors.Is(err, ErrKeyExists) {
		t.Fatal("second create replaced a key")
	}
	if got, _ := m.Get(Task, "t1"); !bytes.Equal(got, first) {
		t.Fatal("original key changed")
	}
	for _, c := range []struct {
		k  Kind
		id string
	}{{"mystery", "x"}, {Task, ""}, {Task, string(make([]byte, 129))}} {
		if _, err := m.Create(c.k, c.id); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("%v accepted", c)
		}
		if _, err := m.Get(c.k, c.id); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("get %v accepted", c)
		}
	}
	if _, err := m.Get(Task, "ghost"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal("missing key")
	}
	// A task key cannot be created before the workspace key exists.
	empty := &Manager{user: bytes.Repeat([]byte{1}, keyLen), wrapped: newWrapped()}
	if _, err := empty.Create(Task, "t"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("no workspace key: %v", err)
	}
	m.Close()
	if _, err := m.Create(Config, "c"); !errors.Is(err, ErrKeystoreUnavailable) {
		t.Fatalf("closed manager: %v", err)
	}
}

func TestResourceKeysAreDeterministicDistinctAndBoundToTheTask(t *testing.T) {
	m, _, _ := manager(t)
	t1, _ := m.Create(Task, "t1")
	t2, _ := m.Create(Task, "t2")
	a, _ := ResourceKey(t1, "res-1")
	b, _ := ResourceKey(t1, "res-1")
	c, _ := ResourceKey(t1, "res-2")
	d, _ := ResourceKey(t2, "res-1")
	if !bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(a, d) || bytes.Equal(a, t1) || len(a) != keyLen {
		t.Fatal("derivation properties")
	}
	for _, bad := range []struct {
		k  []byte
		id string
	}{{t1[:16], "x"}, {t1, ""}, {t1, string(make([]byte, 1025))}} {
		if _, err := ResourceKey(bad.k, bad.id); !errors.Is(err, ErrKeyInvalid) {
			t.Errorf("accepted %d/%d", len(bad.k), len(bad.id))
		}
	}
}

func TestGroupHMACIsKeyedPerBatch(t *testing.T) {
	m, _, _ := manager(t)
	b1, _ := m.Create(Batch, "b1")
	b2, _ := m.Create(Batch, "b2")
	x, _ := GroupHMAC(b1, []byte("13800000000"))
	y, _ := GroupHMAC(b1, []byte("13800000000"))
	z, _ := GroupHMAC(b2, []byte("13800000000"))
	other, _ := GroupHMAC(b1, []byte("13800000001"))
	if !bytes.Equal(x, y) || bytes.Equal(x, z) || bytes.Equal(x, other) || len(x) != 32 {
		t.Fatal("same value, same batch must match; other batch or value must not")
	}
	if bytes.Contains(x, []byte("1380")) {
		t.Fatal("digest carries the value")
	}
	if _, err := GroupHMAC(b1[:8], []byte("v")); !errors.Is(err, ErrKeyInvalid) {
		t.Fatal("short key")
	}
}

func TestCryptoEraseMakesTaskResourcesUnreadable(t *testing.T) {
	m, _, w := manager(t)
	task, _ := m.Create(Task, "t1")
	rk, _ := ResourceKey(task, "res-1")
	var file bytes.Buffer
	wr, err := store.NewResourceWriter(&file, rk, "res-1", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wr.Write([]byte("synthetic private content"))
	wr.Close()
	open := func(key []byte) error {
		r, err := store.OpenResource(bytes.NewReader(file.Bytes()), int64(file.Len()), key, "res-1")
		if err == nil {
			err = r.Verify()
			r.Close()
		}
		return err
	}
	if err := open(rk); err != nil {
		t.Fatalf("before erase: %v", err)
	}
	if err := m.CryptoErase(Task, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(Task, "t1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal("task key survived the erase")
	}
	if _, ok := w.m["task/t1"]; ok {
		t.Fatal("wrapped blob survived")
	}
	// Erase is repeatable, and recreating the same id yields a different key,
	// which cannot open the old ciphertext.
	if err := m.CryptoErase(Task, "t1"); err != nil {
		t.Fatalf("second erase: %v", err)
	}
	again, err := m.Create(Task, "t1")
	if err != nil || bytes.Equal(again, task) {
		t.Fatalf("recreated key: %v", err)
	}
	nk, _ := ResourceKey(again, "res-1")
	if err = open(nk); err == nil {
		t.Fatal("old resource opened with a new key")
	}
	if err = m.CryptoErase("mystery", "x"); !errors.Is(err, ErrKeyInvalid) {
		t.Fatal("erase of an invalid kind")
	}
}

func TestZeroClearsKeys(t *testing.T) {
	k := []byte{1, 2, 3}
	Zero(k)
	if !bytes.Equal(k, []byte{0, 0, 0}) {
		t.Fatal("not cleared")
	}
	m, _, _ := manager(t)
	user := m.user
	m.Close()
	if !bytes.Equal(user, make([]byte, keyLen)) {
		t.Fatal("Close did not clear the user key")
	}
}

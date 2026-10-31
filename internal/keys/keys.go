package keys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

var (
	ErrKeystoreUnavailable = errors.New("KEYSTORE_UNAVAILABLE") // never falls back to plaintext
	ErrKeyNotFound         = errors.New("KEY_NOT_FOUND")
	ErrKeyInvalid          = errors.New("KEY_INVALID")
	ErrKeyExists           = errors.New("KEY_EXISTS")
)

// Kind is a layer of the key hierarchy (architecture 5.1).
type Kind string

const (
	Workspace Kind = "workspace" // wrapped by the user key, encrypts the workspace database
	Config    Kind = "config"    // wrapped by the user key, encrypts the shared config store
	Batch     Kind = "batch"     // wrapped by the workspace key, derives the same-value HMAC key
	Task      Kind = "task"      // wrapped by the workspace key, derives every resource key of a task
)

const keyLen = 32

// UserKeyStore holds the single per-OS-user key (macOS Keychain, Windows
// DPAPI). Implementations return ErrKeystoreUnavailable when the platform
// cannot provide it, such as a session without credentials; callers stop with
// that error and never fall back to a file or to plaintext.
type UserKeyStore interface {
	Load() ([]byte, error) // ErrKeyNotFound when none exists yet
	Save(key []byte) error
	Delete() error
}

// Wrapped keeps wrapped key blobs next to the data they protect (the workspace
// database, or the config store). A blob is useless without the layer above.
type Wrapped interface {
	Load(kind Kind, id string) ([]byte, error) // ErrKeyNotFound if absent
	Save(kind Kind, id string, blob []byte) error
	Delete(kind Kind, id string) error
}

// Manager unwraps and creates keys. It holds the user key in memory only for
// its lifetime; Close zeroes it.
type Manager struct {
	user    []byte
	wrapped Wrapped
}

// Open loads the user key. With create=false a missing key is an error: the
// CLI must not silently start a fresh key hierarchy that cannot read existing
// data (architecture 10). Any store error other than "not found" is reported
// as unavailable.
func Open(store UserKeyStore, wrapped Wrapped, create bool) (*Manager, error) {
	if store == nil || wrapped == nil {
		return nil, ErrKeystoreUnavailable
	}
	k, err := store.Load()
	switch {
	case err == nil:
		if len(k) != keyLen {
			return nil, ErrKeyInvalid
		}
	case errors.Is(err, ErrKeyNotFound):
		if !create {
			return nil, ErrKeyNotFound
		}
		if k, err = random(); err != nil {
			return nil, ErrKeystoreUnavailable
		}
		if err = store.Save(k); err != nil {
			Zero(k)
			return nil, ErrKeystoreUnavailable
		}
	default:
		return nil, ErrKeystoreUnavailable
	}
	return &Manager{user: k, wrapped: wrapped}, nil
}

func (m *Manager) Close() { Zero(m.user); m.user = nil }

// Zero clears a key slice. Callers must do it as soon as a key is no longer
// needed; Go cannot guarantee that no copy remains elsewhere in memory.
func Zero(b []byte) { clear(b) }

func random() ([]byte, error) {
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// parent returns the key that wraps a layer's keys.
func (m *Manager) parent(kind Kind) ([]byte, func(), error) {
	switch kind {
	case Workspace, Config:
		if m.user == nil {
			return nil, nil, ErrKeystoreUnavailable
		}
		return m.user, func() {}, nil
	case Batch, Task:
		ws, err := m.Get(Workspace, "workspace")
		if err != nil {
			return nil, nil, err
		}
		return ws, func() { Zero(ws) }, nil
	}
	return nil, nil, ErrKeyInvalid
}

func aadOf(kind Kind, id string) []byte {
	return []byte("tacet/wrap/v1\x00" + string(kind) + "\x00" + id)
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrKeyInvalid
	}
	return cipher.NewGCM(block)
}

func valid(kind Kind, id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	switch kind {
	case Workspace, Config, Batch, Task:
		return true
	}
	return false
}

// Create generates a new random key for (kind, id), stores it wrapped, and
// returns the plaintext copy for immediate use. An id is never reused: a
// second Create for the same id fails instead of replacing the key, because a
// silent replacement would make existing ciphertext unreadable.
func (m *Manager) Create(kind Kind, id string) ([]byte, error) {
	if !valid(kind, id) {
		return nil, ErrKeyInvalid
	}
	if _, err := m.wrapped.Load(kind, id); err == nil {
		return nil, ErrKeyExists
	} else if !errors.Is(err, ErrKeyNotFound) {
		return nil, err
	}
	parent, release, err := m.parent(kind)
	if err != nil {
		return nil, err
	}
	defer release()
	aead, err := gcm(parent)
	if err != nil {
		return nil, err
	}
	key, err := random()
	if err != nil {
		return nil, ErrKeystoreUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		Zero(key)
		return nil, ErrKeystoreUnavailable
	}
	blob := append([]byte{1}, aead.Seal(nonce, nonce, key, aadOf(kind, id))...)
	if err = m.wrapped.Save(kind, id, blob); err != nil {
		Zero(key)
		return nil, err
	}
	return key, nil
}

// Get unwraps a key. The wrapped blob is bound to its kind and id, so moving a
// blob to another task fails instead of decrypting under the wrong identity.
func (m *Manager) Get(kind Kind, id string) ([]byte, error) {
	if !valid(kind, id) {
		return nil, ErrKeyInvalid
	}
	blob, err := m.wrapped.Load(kind, id)
	if err != nil {
		return nil, err
	}
	parent, release, err := m.parent(kind)
	if err != nil {
		return nil, err
	}
	defer release()
	aead, err := gcm(parent)
	if err != nil {
		return nil, err
	}
	if len(blob) < 1+aead.NonceSize()+aead.Overhead() || blob[0] != 1 {
		return nil, ErrKeyInvalid
	}
	nonce, ct := blob[1:1+aead.NonceSize()], blob[1+aead.NonceSize():]
	key, err := aead.Open(nil, nonce, ct, aadOf(kind, id))
	if err != nil {
		return nil, ErrKeyInvalid
	}
	return key, nil
}

// CryptoErase deletes the wrapped key. Everything encrypted under it (a task's
// resource files) becomes unreadable at once, whatever copies of the
// ciphertext remain on the medium (architecture 5.1; SSDs get no physical
// deletion promise). Erasing a missing key is not an error, so cleanup can be
// retried.
func (m *Manager) CryptoErase(kind Kind, id string) error {
	if !valid(kind, id) {
		return ErrKeyInvalid
	}
	if err := m.wrapped.Delete(kind, id); err != nil && !errors.Is(err, ErrKeyNotFound) {
		return err
	}
	return nil
}

func derive(secret []byte, info string) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, nil, info, keyLen)
}

// ResourceKey derives the key of one workspace resource from the task key and
// the resource id. It travels to the plugin over the pipe and is never stored.
func ResourceKey(taskKey []byte, resourceID string) ([]byte, error) {
	if len(taskKey) != keyLen || resourceID == "" || len(resourceID) > 1024 {
		return nil, ErrKeyInvalid
	}
	return derive(taskKey, "tacet/resource-key/v1\x00"+resourceID)
}

// GroupHMAC computes the keyed digest used for same-value grouping. The key is
// derived from the batch key, so digests of different batches cannot be
// compared and the database never holds an unkeyed hash of a value.
func GroupHMAC(batchKey []byte, value []byte) ([]byte, error) {
	if len(batchKey) != keyLen {
		return nil, ErrKeyInvalid
	}
	k, err := derive(batchKey, "tacet/group-hmac/v1")
	if err != nil {
		return nil, err
	}
	defer Zero(k)
	mac := hmac.New(sha256.New, k)
	mac.Write(value)
	return mac.Sum(nil), nil
}

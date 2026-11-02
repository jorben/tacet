package refs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrNotFound    = errors.New("RESOURCE_NOT_FOUND")
	ErrNotReadable = errors.New("RESOURCE_NOT_READABLE") // unfinished, abandoned or tampered
	ErrForeign     = errors.New("RESOURCE_FOREIGN_JOB")
	ErrRevoked     = errors.New("RESOURCE_REVOKED")
	ErrQuota       = errors.New("RESOURCE_QUOTA_EXCEEDED")
	ErrTampered    = errors.New("RESOURCE_TAMPERED")
	ErrInvalid     = errors.New("RESOURCE_REQUEST_INVALID")
	ErrNotWritable = errors.New("RESOURCE_NOT_WRITABLE")
)

// Mode is the access a plugin gets (architecture 4.4).
type Mode string

const (
	Read  Mode = "read"
	Write Mode = "write" // append-only, a fresh resource with a fresh key
)

// Admit is workspace.Admit: generation, attempt and job version checks.
type Admit func(kernel.AttemptRef) error

// TaskKey returns the task key of a job (keys.Manager.Get with keys.Task).
type TaskKey func(jobID string) ([]byte, error)

type state int

const (
	open state = iota
	frozen
	abandoned
	tampered
	sealing // sealed on disk, not yet in the catalog: not readable
)

type record struct {
	id, job, path string
	version       uint64
	st            state
	digest        string
	size, limit   int64
}

// Manager issues access to workspace resources. Plugins never see paths: they
// get an inherited handle plus the resource key, and only for the duration of
// one attempt. Resource files live in one private directory; ids are random
// hex, so no caller-controlled string reaches the filesystem.
type Manager struct {
	mu      sync.Mutex
	dir     string
	admit   Admit
	key     TaskKey
	res     map[string]*record
	grants  map[kernel.AttemptRef][]*Grant
	revoked map[kernel.AttemptRef]bool
	catalog Catalog
	quota   int64 // per job: finished sizes plus the limits of open writes; 0 = unlimited
}

func New(dir string, admit Admit, key TaskKey) (*Manager, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || admit == nil || key == nil {
		return nil, ErrInvalid
	}
	return &Manager{dir: dir, admit: admit, key: key, res: map[string]*record{},
		grants: map[kernel.AttemptRef][]*Grant{}, revoked: map[kernel.AttemptRef]bool{}}, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func digestOf(path string) (string, int64, error) {
	f, err := platform.OpenShared(path, os.O_RDONLY, 0)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// Adopt registers an existing, finished resource file (such as the encrypted
// input copy or a decrypted copy) as read-only for a job. The digest is
// computed here, never taken from the caller.
func (m *Manager) Adopt(job, id, path string) error {
	if job == "" || id == "" {
		return ErrInvalid
	}
	d, n, err := digestOf(path)
	if err != nil {
		return ErrNotFound
	}
	m.mu.Lock()
	if _, dup := m.res[id]; dup {
		m.mu.Unlock()
		return ErrInvalid
	}
	rec := &record{id: id, job: job, path: path, version: 1, st: sealing, digest: d, size: n}
	m.res[id] = rec
	m.mu.Unlock()
	return m.publish(rec)
}

// Grant is one issued access. The host keeps the file; the supervisor passes it
// to the child through ExtraFiles. Revoke closes the host side and discards an
// unfinished write.
type Grant struct {
	m       *Manager
	attempt kernel.AttemptRef
	mode    Mode
	rec     *record
	file    *os.File
	key     []byte
	ref     string
	revoked bool
}

func (g *Grant) ResourceID() string { return g.rec.id }

func (m *Manager) track(g *Grant) {
	m.grants[g.attempt] = append(m.grants[g.attempt], g)
}

func (m *Manager) resourceKey(job, id string) ([]byte, error) {
	tk, err := m.key(job)
	if err != nil {
		return nil, err
	}
	defer keys.Zero(tk)
	return keys.ResourceKey(tk, id)
}

// IssueRead opens a finished resource of the attempt's own job, read-only. An
// unfinished candidate cannot be read: only frozen resources are visible.
func (m *Manager) IssueRead(a kernel.AttemptRef, id string) (*Grant, error) {
	if err := m.admit(a); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revoked[a] {
		return nil, ErrRevoked
	}
	rec, ok := m.res[id]
	if !ok {
		return nil, ErrNotFound
	}
	if rec.job != a.JobID {
		return nil, ErrForeign
	}
	if rec.st != frozen {
		return nil, ErrNotReadable
	}
	f, err := platform.OpenShared(rec.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, ErrNotFound
	}
	if info, err := f.Stat(); err != nil || info.Size() != rec.size {
		f.Close()
		rec.st = tampered
		return nil, ErrTampered // cheap check here; Verify does the full digest
	}
	key, err := m.resourceKey(rec.job, rec.id)
	if err != nil {
		f.Close()
		return nil, err
	}
	ref, err := randomHex(8)
	if err != nil {
		f.Close()
		keys.Zero(key)
		return nil, ErrInvalid
	}
	g := &Grant{m: m, attempt: a, mode: Read, rec: rec, file: f, key: key, ref: "r_" + ref}
	m.track(g)
	return g, nil
}

// IssueWrite creates a new resource for the attempt: random id, so a fresh key
// (rewrites never reuse a key and nonce space), exclusive 0600 file, append-only.
func (m *Manager) IssueWrite(a kernel.AttemptRef, limit int64) (*Grant, error) {
	if limit <= 0 {
		return nil, ErrInvalid
	}
	if err := m.admit(a); err != nil {
		return nil, err
	}
	idPart, err := randomHex(16)
	if err != nil {
		return nil, ErrInvalid
	}
	id := "res_" + idPart
	path := filepath.Join(m.dir, id)
	f, err := platform.OpenShared(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, ErrNotWritable
	}
	key, err := m.resourceKey(a.JobID, id)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	ref, err := randomHex(8)
	if err != nil {
		f.Close()
		os.Remove(path)
		keys.Zero(key)
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revoked[a] {
		f.Close()
		os.Remove(path)
		keys.Zero(key)
		return nil, ErrRevoked
	}
	if m.quota > 0 && m.used(a.JobID)+limit > m.quota {
		f.Close()
		os.Remove(path)
		keys.Zero(key)
		return nil, ErrQuota
	}
	rec := &record{id: id, job: a.JobID, path: path, st: open, limit: limit}
	m.res[id] = rec
	g := &Grant{m: m, attempt: a, mode: Write, rec: rec, file: f, key: key, ref: "r_" + ref}
	m.track(g)
	return g, nil
}

// Size is the current size of an unfinished write, so the supervisor can stop a
// plugin that exceeds its quota instead of waiting for stream.end.
func (g *Grant) Size() (int64, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.revoked {
		return 0, ErrRevoked
	}
	info, err := os.Stat(g.rec.path)
	if err != nil {
		return 0, ErrNotFound
	}
	return info.Size(), nil
}

// Freeze seals a write after stream.end. It copies ciphertext into a new,
// host-only file and authenticates every encrypted segment before registering
// the digest. Previously inherited writable handles still refer to the old file
// and cannot mutate the frozen resource. The resource identity/key are kept:
// copying ciphertext is not a new encryption operation or nonce reuse.
func (g *Grant) Freeze() (string, error) {
	d, err := g.seal()
	if err != nil {
		return "", err
	}
	if err = g.m.publish(g.rec); err != nil {
		return "", err
	}
	return d, nil
}

// seal copies and authenticates the ciphertext under refs.mu and leaves the
// record in the sealing state; publish makes it readable once it is cataloged.
func (g *Grant) seal() (string, error) {
	m := g.m
	if g.mode != Write {
		return "", ErrNotWritable
	}
	// Admission may take the workspace commit lock; never call it while holding
	// refs.mu (cancellation can revoke references from the commit section).
	admissionErr := m.admit(g.attempt)
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.revoked || g.rec.st != open || m.revoked[g.attempt] {
		return "", ErrRevoked
	}
	if admissionErr != nil {
		m.discard(g)
		return "", admissionErr
	}
	if err := g.file.Sync(); err != nil {
		m.discard(g)
		return "", ErrNotWritable
	}
	oldPath := g.rec.path
	input, err := platform.OpenShared(oldPath, os.O_RDONLY, 0)
	if err != nil {
		m.discard(g)
		return "", ErrNotWritable
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		m.discard(g)
		return "", ErrNotWritable
	}
	if info.Size() > g.rec.limit {
		m.discard(g)
		return "", ErrQuota
	}
	sealed, err := os.CreateTemp(m.dir, ".frozen-")
	if err != nil {
		m.discard(g)
		return "", ErrNotWritable
	}
	sealedPath := sealed.Name()
	registered := false
	defer func() {
		sealed.Close()
		if !registered {
			os.Remove(sealedPath)
		}
	}()
	// The observed size bounds the copy even if the plugin continues appending.
	// Any torn/reordered segment in that snapshot fails GCM verification below.
	hash := sha256.New()
	n, err := io.CopyN(io.MultiWriter(sealed, hash), input, info.Size())
	if err != nil || n != info.Size() {
		m.discard(g)
		return "", ErrNotWritable
	}
	reader, err := store.OpenResource(sealed, n, g.key, g.rec.id)
	if err == nil {
		err = reader.Verify()
		reader.Close()
	}
	if err != nil {
		m.discard(g)
		return "", ErrTampered
	}
	if err := sealed.Chmod(0o400); err != nil {
		m.discard(g)
		return "", ErrNotWritable
	}
	if err := sealed.Sync(); err != nil {
		m.discard(g)
		return "", ErrNotWritable
	}
	d := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	g.release()
	input.Close()
	if err := os.Remove(oldPath); err != nil {
		g.rec.st = abandoned
		return "", ErrNotWritable
	}
	g.rec.path, g.rec.st = sealedPath, sealing
	g.rec.digest, g.rec.size, g.rec.version = d, n, 1
	registered = true
	return d, nil
}

func (g *Grant) release() {
	g.file.Close()
	keys.Zero(g.key)
	g.revoked = true
}

// discard drops an unfinished write: partial output of an attempt never stays.
func (m *Manager) discard(g *Grant) {
	if !g.revoked {
		g.release()
	}
	if g.rec.st == open {
		g.rec.st = abandoned
		os.Remove(g.rec.path)
	}
}

// Revoke ends all access of one attempt: host handles close, keys are zeroed,
// unfinished writes are deleted. Frozen resources survive. It reports how many
// grants it ended. Attempts are keyed by job, attempt, owner generation and
// job version. The supervisor must still reclaim inherited handles by ending
// their process tree; frozen resources use a separate host-only file.
func (m *Manager) Revoke(a kernel.AttemptRef) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[a] = true
	n := 0
	for _, g := range m.grants[a] {
		if g.mode == Write {
			m.discard(g)
		} else if !g.revoked {
			g.release()
		}
		n++
	}
	delete(m.grants, a)
	return n
}

// Verify recomputes the digest of a finished resource. Run it before the
// candidate is validated or published to detect storage modification (INV-04).
// A plugin's old writable handle cannot reach the sealed file created at Freeze.
func (m *Manager) Verify(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.res[id]
	if !ok {
		return ErrNotFound
	}
	if rec.st != frozen {
		return ErrNotReadable
	}
	d, n, err := digestOf(rec.path)
	if err != nil {
		return ErrNotFound
	}
	if d != rec.digest || n != rec.size {
		rec.st = tampered
		return ErrTampered
	}
	return nil
}

// Digest of a finished resource.
func (m *Manager) Digest(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.res[id]
	if !ok {
		return "", ErrNotFound
	}
	if rec.st != frozen {
		return "", ErrNotReadable
	}
	return rec.digest, nil
}

// Encryption is the key material sent to the plugin over the pipe only.
type Encryption struct {
	Version    int    `json:"version"`
	ResourceID string `json:"resource_id"`
	KeyHex     string `json:"key_hex"`
}

// Wire is the protocol ResourceRef (common.schema.json). It contains no path.
type Wire struct {
	Ref        string      `json:"ref"`
	Mode       Mode        `json:"mode"`
	Handle     int         `json:"handle"`
	Digest     string      `json:"digest,omitempty"`
	Version    uint64      `json:"version,omitempty"`
	Encryption *Encryption `json:"encryption"`
}

// Handoff lists the files for the child's ExtraFiles and the matching wire
// references: handle numbers start at 3 in order, as os/exec assigns them.
func Handoff(grants ...*Grant) ([]*os.File, []Wire, error) {
	files := make([]*os.File, 0, len(grants))
	wires := make([]Wire, 0, len(grants))
	for i, g := range grants {
		if g == nil || g.m == nil {
			return nil, nil, ErrInvalid
		}
		file, wire, err := g.handoff(3 + i)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, file)
		wires = append(wires, wire)
	}
	return files, wires, nil
}

func (g *Grant) handoff(handle int) (*os.File, Wire, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.revoked || g.m.revoked[g.attempt] || g.file == nil || len(g.key) == 0 || allZero(g.key) {
		return nil, Wire{}, ErrRevoked
	}
	wire := Wire{Ref: g.ref, Mode: g.mode, Handle: handle,
		Encryption: &Encryption{Version: 1, ResourceID: g.rec.id, KeyHex: hex.EncodeToString(g.key)}}
	if g.mode == Read {
		wire.Digest, wire.Version = g.rec.digest, g.rec.version
	}
	return g.file, wire, nil
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// HostWriter lets the host itself write the content of one of its own write
// grants, such as the operation plan it hands to a plugin. The data is encrypted
// under the resource's own key exactly like a plugin's output; close the writer
// before Freeze. A grant that is handed to a plugin must not also be written here.
func (g *Grant) HostWriter(limit uint64) (*store.ResourceWriter, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.mode != Write {
		return nil, ErrNotWritable
	}
	if g.revoked || g.file == nil || g.rec.st != open || len(g.key) == 0 || allZero(g.key) {
		return nil, ErrRevoked
	}
	if limit == 0 || int64(limit) > g.rec.limit {
		return nil, ErrInvalid
	}
	w, err := store.NewResourceWriter(g.file, g.key, g.rec.id, limit)
	if err != nil {
		return nil, ErrNotWritable
	}
	return w, nil
}

// Plaintext opens a read grant's resource decrypted, for the one module that may
// write the content to a user-visible place. Every segment is authenticated as it
// is read, so a resource that was altered fails the read rather than yielding other
// bytes. Close releases only the reader; the grant is still the caller's to revoke.
func (g *Grant) Plaintext() (io.Reader, int64, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.mode != Read {
		return nil, 0, ErrNotReadable
	}
	if g.revoked || g.file == nil || len(g.key) == 0 || allZero(g.key) {
		return nil, 0, ErrRevoked
	}
	info, err := g.file.Stat()
	if err != nil {
		return nil, 0, ErrNotFound
	}
	r, err := store.OpenResource(g.file, info.Size(), g.key, g.rec.id)
	if err != nil {
		return nil, 0, ErrTampered
	}
	return io.NewSectionReader(r, 0, r.Size()), r.Size(), nil
}

// ReadBack returns the decrypted content of a write grant that is still open, for a small
// transient result the host shows and never keeps (a page preview). The plugin that was
// handed the grant must have ended first; the resource is deleted with the grant when its
// attempt is revoked, and it never enters the catalog. Every segment is authenticated.
func (g *Grant) ReadBack(limit int64) ([]byte, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.mode != Write || g.rec.st != open {
		return nil, ErrNotWritable
	}
	if g.revoked || len(g.key) == 0 || allZero(g.key) {
		return nil, ErrRevoked
	}
	f, err := platform.OpenShared(g.rec.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, ErrNotFound
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, ErrNotFound
	}
	r, err := store.OpenResource(f, info.Size(), g.key, g.rec.id)
	if err != nil {
		return nil, ErrTampered
	}
	if limit <= 0 || r.Size() > limit {
		return nil, ErrQuota
	}
	buf := make([]byte, r.Size())
	if _, err = r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return nil, ErrTampered
	}
	return buf, nil
}

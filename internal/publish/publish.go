package publish

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/verdict"
)

var (
	ErrInvalid           = errors.New("PUBLISH_INVALID")
	ErrNotReady          = errors.New("PUBLISH_NOT_READY")
	ErrNoGrant           = errors.New("PUBLISH_NOT_AUTHORIZED")
	ErrStale             = errors.New("PUBLISH_STALE")
	ErrDigest            = errors.New("PUBLISH_DIGEST_MISMATCH")
	ErrNameConflict      = errors.New("PUBLISH_NAME_CONFLICT")
	ErrAtomicUnavailable = errors.New("PUBLISH_ATOMIC_UNAVAILABLE")
	ErrIO                = errors.New("PUBLISH_IO_FAILED")
	ErrStore             = errors.New("PUBLISH_STORE_FAILED")
)

// Module owns grants, publish intents and delivery records. Rows hold ids,
// names, digests and states, never content. Names are neutral output names
// chosen at preflight, not source names (FR-36).
func Module() store.Module {
	return store.Module{Name: "publish", Prefix: "publish_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE publish_grant(job TEXT PRIMARY KEY, key TEXT NOT NULL, source TEXT NOT NULL, partial INTEGER NOT NULL, at INTEGER NOT NULL)`,
		`CREATE TABLE publish_intent(id TEXT PRIMARY KEY, job TEXT NOT NULL, delivery INTEGER NOT NULL, state TEXT NOT NULL
			CHECK(state IN ('prepared','published','conflict','aborted')), dir TEXT NOT NULL, name TEXT NOT NULL, temp TEXT NOT NULL,
			digest TEXT NOT NULL, partial INTEGER NOT NULL, atomic INTEGER NOT NULL)`,
		`CREATE INDEX publish_intent_state ON publish_intent(state, job)`,
		`CREATE TABLE publish_delivery(job TEXT NOT NULL, version INTEGER NOT NULL, dir TEXT NOT NULL, name TEXT NOT NULL, digest TEXT NOT NULL,
			partial INTEGER NOT NULL, atomic INTEGER NOT NULL, superseded INTEGER NOT NULL DEFAULT 0, at INTEGER NOT NULL, PRIMARY KEY(job, version))`,
	}}}}
}

// Grant sources (module design 8.4).
const (
	Explicit = "explicit" // quick-path confirm, guided batch summary, "publish eligible files"
	Standing = "standing" // unattended mode rule frozen in the delivery facet
)

// PartialMarker is what a partial result's output name must carry.
const PartialMarker = "-partial"

// MaxBytes bounds one published file.
var MaxBytes int64 = 8 << 30

type Commit func(ctx context.Context, fn func(store.Tx) error) error

// Service writes to user-visible directories. It is the only module that does.
type Service struct {
	Commit Commit
	// Admit is workspace.AdmitTx: the attempt must still own the job.
	Admit func(tx store.Tx, a kernel.AttemptRef) error
	// CurrentKey returns the job's current disposition key inside tx.
	CurrentKey func(tx store.Tx, job string) (string, error)
	Now        func() time.Time
	// Placed, when set, is called once the file is in place and before its delivery is recorded: the
	// window a crash leaves for Recover. Only tests set it; it must not block in production.
	Placed func()

	rename func(temporary, destination string) error // platform.PublishExclusive; replaceable in tests
}

func (s *Service) ok() bool {
	return s != nil && s.Commit != nil && s.Admit != nil && s.CurrentKey != nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) renameFn() func(string, string) error {
	if s.rename != nil {
		return s.rename
	}
	return platform.PublishExclusive
}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := t.Module("publish")
	return t, sc, err
}

var reserved = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true, "lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true}

// ValidName accepts a plain file name that is safe on both platforms: no path
// parts, no control characters, no reserved device names, no trailing dot or
// space, and not our own temporary prefix.
func ValidName(n string) bool {
	if n == "" || len(n) > 200 || !utf8.ValidString(n) || n == "." || n == ".." || strings.HasPrefix(n, tempPrefix) {
		return false
	}
	if strings.ContainsAny(n, `/\:*?"<>|`) || strings.HasSuffix(n, ".") || strings.HasSuffix(n, " ") {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	stem := strings.ToLower(n)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	return !reserved[strings.TrimRight(stem, " ")]
}

func validDir(d string) bool {
	if d == "" || !filepath.IsAbs(d) {
		return false
	}
	fi, err := os.Lstat(d)
	return err == nil && fi.IsDir()
}

const tempPrefix = ".tacet-"

func randomName() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", ErrIO
	}
	return tempPrefix + hex.EncodeToString(b) + ".tmp", nil
}

// Grant records the authorization to publish the job under a disposition key.
// A later key (new finding, changed decision, changed dispose facet) makes it
// invalid without any further action (MD-16). Only the grant of the job is
// affected; other members keep theirs (BUX-12).
func (s *Service) Grant(ctx context.Context, job, key, source string, partial bool) error {
	if !s.ok() || job == "" || key == "" || (source != Explicit && source != Standing) {
		return ErrInvalid
	}
	err := s.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO publish_grant(job, key, source, partial, at) VALUES(?,?,?,?,?)
			ON CONFLICT(job) DO UPDATE SET key=excluded.key, source=excluded.source, partial=excluded.partial, at=excluded.at`,
			job, key, source, b2i(partial), s.now().Unix())
		return err
	})
	if err != nil {
		return ErrStore
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type grantRow struct {
	key     string
	partial bool
}

func loadGrant(t *store.Txn, job string) (grantRow, bool, error) {
	var g grantRow
	var p int
	switch err := t.QueryRow("SELECT key, partial FROM publish_grant WHERE job=?", job).Scan(&g.key, &p); {
	case errors.Is(err, sql.ErrNoRows):
		return g, false, nil
	case err != nil:
		return g, false, err
	}
	g.partial = p != 0
	return g, true, nil
}

// Revoke withdraws a job's grant.
func (s *Service) Revoke(ctx context.Context, job string) error {
	if !s.ok() || job == "" {
		return ErrInvalid
	}
	if err := s.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec("DELETE FROM publish_grant WHERE job=?", job)
		return err
	}); err != nil {
		return ErrStore
	}
	return nil
}

// Request is one publication.
type Request struct {
	Attempt kernel.AttemptRef
	Key     string        // the disposition key the candidate was made under
	Facts   verdict.Facts // what the gate judges
	// Candidate yields the decrypted candidate bytes; Digest is "sha256:<hex>".
	Candidate io.Reader
	Digest    string
	Dir, Name string
	// AllowNonAtomic accepts the fallback of FR-30 on volumes without an atomic
	// no-replace rename; the user was told beforehand.
	AllowNonAtomic bool
}

// Delivery is the record of a published file.
type Delivery struct {
	Job     string
	Version int
	Dir     string
	Name    string
	Digest  string
	Partial bool
	Atomic  bool
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", ErrIO
	}
	return "pub_" + hex.EncodeToString(b), nil
}

// Publish copies the verified candidate into the target directory without ever
// replacing anything (module design 8.4). Gate, grant and copy happen before the
// serial section; inside it the input, key, grant and ownership are checked
// again, the intent is made durable and only then is the file renamed into place.
func (s *Service) Publish(ctx context.Context, r Request) (Delivery, error) {
	var zero Delivery
	job := r.Attempt.JobID
	if !s.ok() || job == "" || r.Key == "" || r.Candidate == nil || !validDir(r.Dir) || !ValidName(r.Name) || !strings.HasPrefix(r.Digest, "sha256:") {
		return zero, ErrInvalid
	}
	readiness, reason := verdict.PublishReadiness(r.Facts)
	if readiness == verdict.NotReady {
		return zero, fmt.Errorf("%w: %s", ErrNotReady, reason)
	}
	partial := readiness == verdict.ReadyPartial
	if partial && !strings.Contains(r.Name, PartialMarker) {
		return zero, ErrInvalid // a partial result must be recognisable by its name
	}
	if err := s.checkGrant(ctx, job, r.Key, partial); err != nil {
		return zero, err
	}
	temp, err := copyTemp(r.Dir, r.Candidate, r.Digest)
	if err != nil {
		return zero, err
	}
	target := filepath.Join(r.Dir, r.Name)
	id, err := newID()
	if err != nil {
		os.Remove(temp)
		return zero, err
	}
	var version int
	err = s.Commit(ctx, func(tx store.Tx) error {
		if err := s.Admit(tx, r.Attempt); err != nil {
			return ErrStale
		}
		t, sc, err := scope(tx)
		if err != nil {
			return err
		}
		cur, err := s.CurrentKey(tx, job)
		if err != nil || cur != r.Key {
			return ErrStale
		}
		g, ok, err := loadGrant(t, job)
		if err != nil {
			return err
		}
		if !ok || g.key != r.Key || (partial && !g.partial) {
			return ErrNoGrant
		}
		if err = t.QueryRow("SELECT COALESCE(MAX(version),0)+1 FROM publish_delivery WHERE job=?", job).Scan(&version); err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO publish_intent(id, job, delivery, state, dir, name, temp, digest, partial, atomic) VALUES(?,?,?,'prepared',?,?,?,?,?,1)`,
			id, job, version, r.Dir, r.Name, filepath.Base(temp), r.Digest, b2i(partial))
		return err
	})
	if err != nil {
		os.Remove(temp)
		return zero, mapCommitErr(err)
	}
	// From here on the outcome must be recorded even if the caller gives up.
	bg := context.WithoutCancel(ctx)
	atomic := true
	switch err = s.renameFn()(temp, target); {
	case err == nil:
	case errors.Is(err, platform.ErrPublishExists):
		os.Remove(temp)
		s.settle(bg, id, "conflict", true)
		return zero, ErrNameConflict
	case errors.Is(err, platform.ErrPublishUnavailable):
		if !r.AllowNonAtomic {
			os.Remove(temp)
			s.settle(bg, id, "aborted", true)
			return zero, ErrAtomicUnavailable
		}
		atomic = false
		if err = exclusiveCopy(temp, target, r.Digest); err != nil {
			os.Remove(temp)
			if errors.Is(err, ErrNameConflict) {
				s.settle(bg, id, "conflict", true)
			} else {
				s.settle(bg, id, "aborted", true)
			}
			return zero, err
		}
	default:
		os.Remove(temp)
		s.settle(bg, id, "aborted", true)
		return zero, ErrIO
	}
	syncDir(r.Dir)
	if s.Placed != nil {
		s.Placed()
	}
	d := Delivery{Job: job, Version: version, Dir: r.Dir, Name: r.Name, Digest: r.Digest, Partial: partial, Atomic: atomic}
	if err = s.Commit(bg, func(tx store.Tx) error { return s.markPublished(tx, id, d) }); err != nil {
		// The file is in place and the intent is still "prepared": Recover finishes it.
		return d, ErrStore
	}
	return d, nil
}

func mapCommitErr(err error) error {
	switch {
	case errors.Is(err, ErrStale), errors.Is(err, ErrNoGrant), errors.Is(err, ErrInvalid):
		return err
	}
	return ErrStore
}

func (s *Service) checkGrant(ctx context.Context, job, key string, partial bool) error {
	var ok bool
	var g grantRow
	if err := s.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		g, ok, err = loadGrant(t, job)
		return err
	}); err != nil {
		return ErrStore
	}
	if !ok || g.key != key || (partial && !g.partial) {
		return ErrNoGrant
	}
	return nil
}

// settle ends an intent that did not produce a delivery.
func (s *Service) settle(ctx context.Context, id, state string, _ bool) {
	_ = s.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec("UPDATE publish_intent SET state=? WHERE id=? AND state='prepared'", state, id)
		return err
	})
}

func (s *Service) markPublished(tx store.Tx, id string, d Delivery) error {
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	res, err := sc.Exec("UPDATE publish_intent SET state='published', atomic=? WHERE id=? AND state='prepared'", b2i(d.Atomic), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil // already settled by a recovery: nothing to add
	}
	if _, err = sc.Exec("UPDATE publish_delivery SET superseded=1 WHERE job=?", d.Job); err != nil {
		return err
	}
	if _, err = sc.Exec(`INSERT INTO publish_delivery(job, version, dir, name, digest, partial, atomic, at) VALUES(?,?,?,?,?,?,?,?)`,
		d.Job, d.Version, d.Dir, d.Name, d.Digest, b2i(d.Partial), b2i(d.Atomic), s.now().Unix()); err != nil {
		return err
	}
	tx.Emit(store.Event{Type: "Published", Subject: kernel.ArtifactRef{Kind: "delivery", ID: d.Job, Version: uint64(d.Version), Digest: d.Digest}})
	return nil
}

// copyTemp streams the candidate into an exclusively created, owner-only
// temporary file in dir, flushes it and checks the digest.
func copyTemp(dir string, src io.Reader, digest string) (string, error) {
	name, err := randomName()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", ErrIO
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, MaxBytes+1))
	if err == nil && n > MaxBytes {
		err = ErrIO
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return "", ErrIO
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest {
		os.Remove(path)
		return "", ErrDigest
	}
	return path, nil
}

// exclusiveCopy is the non-atomic fallback of FR-30: create the target
// exclusively, copy, flush and verify; a failure removes what it created.
func exclusiveCopy(temp, target, digest string) error {
	in, err := os.Open(temp)
	if err != nil {
		return ErrIO
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrNameConflict
		}
		return ErrIO
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest {
		err = ErrDigest
	}
	if err != nil {
		os.Remove(target)
		return ErrIO
	}
	os.Remove(temp)
	return nil
}

func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Recovered says what start-up did with an intent left "prepared".
type Recovered struct {
	Job     string
	Name    string
	Outcome string // "published", "conflict" or "aborted"
}

// Recover settles every prepared intent after a crash (architecture 6.1):
// the target exists with the intended digest → record the delivery; it exists
// with another digest → conflict (never overwritten, never republished); it does
// not exist → delete the leftover temporary file and abort, so the job returns
// to ready_to_publish and the gate is checked again.
func (s *Service) Recover(ctx context.Context) ([]Recovered, error) {
	if !s.ok() {
		return nil, ErrInvalid
	}
	type row struct {
		id, job, dir, name, temp, digest string
		version, partial                 int
	}
	var rows []row
	if err := s.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		rs, err := t.Query("SELECT id, job, delivery, dir, name, temp, digest, partial FROM publish_intent WHERE state='prepared' ORDER BY id")
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var x row
			if err = rs.Scan(&x.id, &x.job, &x.version, &x.dir, &x.name, &x.temp, &x.digest, &x.partial); err != nil {
				return err
			}
			rows = append(rows, x)
		}
		return rs.Err()
	}); err != nil {
		return nil, ErrStore
	}
	var out []Recovered
	for _, x := range rows {
		target := filepath.Join(x.dir, x.name)
		outcome := "aborted"
		if got, err := fileDigest(target); err == nil {
			outcome = "conflict"
			if got == x.digest {
				outcome = "published"
			}
		}
		if outcome != "published" && ValidName(x.name) {
			// the leftover temporary file is ours only if it has our name
			if strings.HasPrefix(x.temp, tempPrefix) {
				os.Remove(filepath.Join(x.dir, x.temp))
			}
		}
		x := x
		if err := s.Commit(ctx, func(tx store.Tx) error {
			if outcome == "published" {
				return s.markPublished(tx, x.id, Delivery{Job: x.job, Version: x.version, Dir: x.dir, Name: x.name, Digest: x.digest, Partial: x.partial != 0, Atomic: true})
			}
			_, sc, err := scope(tx)
			if err != nil {
				return err
			}
			_, err = sc.Exec("UPDATE publish_intent SET state=? WHERE id=? AND state='prepared'", outcome, x.id)
			return err
		}); err != nil {
			return out, ErrStore
		}
		if outcome == "published" && x.temp != "" {
			os.Remove(filepath.Join(x.dir, x.temp)) // renamed already; harmless if gone
		}
		out = append(out, Recovered{Job: x.job, Name: x.name, Outcome: outcome})
	}
	return out, nil
}

// Deliveries lists a job's delivery records, newest first. Superseded ones are
// kept (architecture 6).
func Deliveries(tx store.Tx, job string) ([]Delivery, []bool, error) {
	if job == "" {
		return nil, nil, ErrInvalid
	}
	t, _, err := scope(tx)
	if err != nil {
		return nil, nil, err
	}
	rs, err := t.Query("SELECT version, dir, name, digest, partial, atomic, superseded FROM publish_delivery WHERE job=? ORDER BY version DESC", job)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	var ds []Delivery
	var sup []bool
	for rs.Next() {
		d := Delivery{Job: job}
		var p, a, s int
		if err = rs.Scan(&d.Version, &d.Dir, &d.Name, &d.Digest, &p, &a, &s); err != nil {
			return nil, nil, err
		}
		d.Partial, d.Atomic = p != 0, a != 0
		ds, sup = append(ds, d), append(sup, s != 0)
	}
	return ds, sup, rs.Err()
}

// Purge deletes a job's grant, intents and delivery records (retention). The
// delivered files themselves belong to the user and are never touched.
func Purge(tx store.Tx, job string) error {
	if job == "" {
		return ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM publish_grant WHERE job=?", "DELETE FROM publish_intent WHERE job=?", "DELETE FROM publish_delivery WHERE job=?"} {
		if _, err = sc.Exec(q, job); err != nil {
			return err
		}
	}
	return nil
}

// Artifacts writes report and manifest files with exclusive creation.
type Artifacts struct{ Dir string }

// WriteArtifact never overwrites. The same name with the same bytes is already
// written and succeeds, so replaying an event is safe; the same name with other
// bytes is a conflict.
func (a Artifacts) WriteArtifact(ctx context.Context, name string, data []byte) error {
	if !validDir(a.Dir) || !ValidName(name) || int64(len(data)) > MaxBytes {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	target := filepath.Join(a.Dir, name)
	temp, err := copyTemp(a.Dir, strings.NewReader(string(data)), want)
	if err != nil {
		return err
	}
	switch err = platform.PublishExclusive(temp, target); {
	case err == nil:
		syncDir(a.Dir)
		return nil
	case errors.Is(err, platform.ErrPublishExists):
		os.Remove(temp)
		if got, derr := fileDigest(target); derr == nil && got == want {
			return nil
		}
		return ErrNameConflict
	case errors.Is(err, platform.ErrPublishUnavailable):
		defer os.Remove(temp)
		if err = exclusiveCopy(temp, target, want); errors.Is(err, ErrNameConflict) {
			if got, derr := fileDigest(target); derr == nil && got == want {
				return nil
			}
		}
		return err
	}
	os.Remove(temp)
	return ErrIO
}

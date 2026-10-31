package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrSource        = errors.New("PREFLIGHT_SOURCE_INVALID")
	ErrTargetAlias   = errors.New("PREFLIGHT_TARGET_IS_SOURCE")
	ErrProbe         = errors.New("PREFLIGHT_PROBE_INVALID")
	ErrUnlock        = errors.New("PREFLIGHT_UNLOCK_FAILED")
	ErrPasswordWrong = errors.New("PASSWORD_INCORRECT")
)

// FileID is a volume's identity of a file (platform.FileID, kept here so this
// module needs no platform import; app wires Identify).
type FileID struct{ Volume, Index, Links uint64 }

func (a FileID) Same(b FileID) bool { return a.Volume == b.Volume && a.Index == b.Index }

// Identify returns the identity of what a path names, following symlinks.
type Identify func(path string) (FileID, error)

// Source is the read-only fact sheet of one input (design 6.1 steps 1 and 2).
type Source struct {
	Real            string // symlinks resolved
	SymlinkResolved bool   // the given path was not the real path
	ID              FileID
	Size            int64
	ModTime         time.Time
	Fast            string // fast fingerprint; the full digest is computed in the background
}

// Inspect resolves the real path and takes the fast fingerprint. It never opens
// the file for reading and never follows a path it cannot resolve.
func Inspect(path string, identify Identify) (Source, error) {
	if identify == nil || path == "" || !filepath.IsAbs(path) {
		return Source{}, ErrSource
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Source{}, ErrSource
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return Source{}, ErrSource
	}
	id, err := identify(real)
	if err != nil {
		return Source{}, ErrSource
	}
	h := sha256.New()
	fmt.Fprintf(h, "tacet/fast/v1\x00%d\x00%d\x00%d\x00%d", info.Size(), info.ModTime().UnixNano(), id.Volume, id.Index)
	return Source{Real: real, SymlinkResolved: real != filepath.Clean(path), ID: id, Size: info.Size(), ModTime: info.ModTime(),
		Fast: "fast:" + hex.EncodeToString(h.Sum(nil))}, nil
}

// Changed reports whether the file is no longer what was inspected (FR-08): a
// different size, time or identity, or a path that no longer resolves.
func (s Source) Changed(identify Identify) bool {
	now, err := Inspect(s.Real, identify)
	return err != nil || now.Fast != s.Fast
}

// CheckTarget refuses an output target that is the source itself (FR-03): the
// same path, a symlink to it, a hard link to it, or a case variant on a
// case-insensitive volume (found by identity because the variant exists). A target that does not exist yet is checked through
// its parent directory, which must resolve and must not be the source.
func CheckTarget(src Source, target string, identify Identify) error {
	if identify == nil || target == "" || !filepath.IsAbs(target) {
		return ErrSource
	}
	clean := filepath.Clean(target)
	if id, err := identify(clean); err == nil {
		if id.Same(src.ID) {
			return ErrTargetAlias
		}
		return nil
	}
	if _, err := filepath.EvalSymlinks(filepath.Dir(clean)); err != nil {
		return ErrSource
	}
	// A target that does not exist cannot be the source or a link to it. A case
	// variant of the source's name that does not exist is a different file on a
	// case-sensitive volume; on a case-insensitive one it would exist and have
	// been caught by identity above. The parent only has to resolve.
	return nil
}

// Encryption is the probe's finding (architecture 4.6).
type Encryption string

const (
	None             Encryption = "none"
	PasswordRequired Encryption = "password_required"
	PermissionOnly   Encryption = "permission_only"
	Unsupported      Encryption = "unsupported"
)

// Probe is what a format plugin's budgeted probe returns.
type Probe struct {
	Format     string
	Encryption Encryption
	Pages      int
	Images     int
	Embedded   int
	Bytes      int64
}

// Limits are the initial protection values of requirements 5.4; releases freeze
// them per format plugin and resource tier.
type Limits struct {
	MaxPages int
	MaxBytes int64
}

func DefaultLimits() Limits { return Limits{MaxPages: 2000, MaxBytes: 8 << 30} }

// State is the preflight verdict of one member.
type State string

const (
	Preflighted   State = "preflighted"
	Locked        State = "locked"
	Unprocessable State = "unprocessable"
)

// Reason codes for Unprocessable and notes for the others.
const (
	ReasonFormat      = "FORMAT_NOT_SUPPORTED"
	ReasonPages       = "PAGE_LIMIT_EXCEEDED"
	ReasonBytes       = "SIZE_LIMIT_EXCEEDED"
	ReasonEncryption  = "ENCRYPTION_UNSUPPORTED"
	NoteEncryptedOnly = "PERMISSION_PASSWORD_ONLY"
	NoteOutputPlain   = "SOURCE_ENCRYPTED_OUTPUT_PLAIN"
)

// Outcome carries the actual values and the limit, so the user is told both.
type Outcome struct {
	State  State
	Reason string
	Actual int64
	Limit  int64
	Notes  []string
}

// Decide is pure. A format the registry did not accept, a probe that violates
// its own bounds, and every over-limit value are refused rather than guessed.
func Decide(p Probe, accepted map[string]bool, l Limits) (Outcome, error) {
	if p.Format == "" || p.Pages < 0 || p.Images < 0 || p.Embedded < 0 || p.Bytes < 0 {
		return Outcome{}, ErrProbe
	}
	switch p.Encryption {
	case None, PasswordRequired, PermissionOnly, Unsupported:
	default:
		return Outcome{}, ErrProbe
	}
	if !accepted[p.Format] {
		return Outcome{State: Unprocessable, Reason: ReasonFormat}, nil
	}
	if p.Encryption == Unsupported {
		return Outcome{State: Unprocessable, Reason: ReasonEncryption}, nil
	}
	if p.Pages > l.MaxPages {
		return Outcome{State: Unprocessable, Reason: ReasonPages, Actual: int64(p.Pages), Limit: int64(l.MaxPages)}, nil
	}
	if p.Bytes > l.MaxBytes {
		return Outcome{State: Unprocessable, Reason: ReasonBytes, Actual: p.Bytes, Limit: l.MaxBytes}, nil
	}
	switch p.Encryption {
	case PasswordRequired:
		return Outcome{State: Locked, Notes: []string{NoteOutputPlain}}, nil
	case PermissionOnly:
		return Outcome{State: Preflighted, Notes: []string{NoteEncryptedOnly}}, nil
	}
	return Outcome{State: Preflighted}, nil
}

// Unlocker decrypts an input into a task-encrypted workspace resource. The
// password is the only thing that crosses; the returned reference names the
// decrypted copy. ErrPasswordWrong means "try again".
type Unlocker interface {
	Unlock(ctx context.Context, input string, password []byte) (copyRef string, err error)
}

// Unlock hands the password to the unlocker and zeroes it afterwards on every
// path: success, wrong password, failure and panic. The caller must not reuse
// the slice. A wrong password is reported as such and never as a failure of the
// file, so it does not count as a reprocessing.
func Unlock(ctx context.Context, u Unlocker, input string, password []byte) (copyRef string, err error) {
	defer clear(password)
	if u == nil || input == "" || len(password) == 0 {
		return "", ErrUnlock
	}
	ref, err := u.Unlock(ctx, input, password)
	switch {
	case err == nil && ref != "":
		return ref, nil
	case errors.Is(err, ErrPasswordWrong):
		return "", ErrPasswordWrong
	}
	return "", ErrUnlock // no unlocker text is passed on: it could quote the password
}

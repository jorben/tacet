//go:build darwin

package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/platform"
)

func identify(p string) (FileID, error) {
	id, err := platform.Identify(p)
	return FileID{Volume: id.Volume, Index: id.Index, Links: id.Links}, err
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInspectResolvesSymlinksAndFingerprintsSizeTimeAndIdentity(t *testing.T) {
	dir := realDir(t)
	src := write(t, dir, "a.docx", "hello")
	link := filepath.Join(dir, "link.docx")
	os.Symlink(src, link)
	s, err := Inspect(link, identify)
	if err != nil || s.Real != src || !s.SymlinkResolved || s.Size != 5 || !strings.HasPrefix(s.Fast, "fast:") {
		t.Fatalf("%+v %v", s, err)
	}
	plain, _ := Inspect(src, identify)
	if plain.SymlinkResolved || plain.Fast != s.Fast {
		t.Fatal("the same file must fingerprint the same however it was named")
	}
	for name, p := range map[string]string{"relative": "a.docx", "empty": "", "missing": filepath.Join(dir, "no"), "directory": dir} {
		if _, err := Inspect(p, identify); !errors.Is(err, ErrSource) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Inspect(src, nil); !errors.Is(err, ErrSource) {
		t.Error("no identity function")
	}
	os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling"))
	if _, err := Inspect(filepath.Join(dir, "dangling"), identify); !errors.Is(err, ErrSource) {
		t.Error("dangling symlink")
	}
}

func TestChangedDetectsContentSizeTimeReplacementAndRemoval(t *testing.T) {
	dir := realDir(t)
	src := write(t, dir, "a.docx", "hello")
	s, _ := Inspect(src, identify)
	if s.Changed(identify) {
		t.Fatal("an untouched file reads as changed")
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(src, future, future)
	if !s.Changed(identify) {
		t.Fatal("a changed modification time was missed")
	}
	s, _ = Inspect(src, identify)
	write(t, dir, "a.docx", "hello world")
	if !s.Changed(identify) {
		t.Fatal("a changed size was missed")
	}
	s, _ = Inspect(src, identify)
	// replaced by another file of the same size and time: only the identity differs
	info, _ := os.Stat(src)
	other := write(t, dir, "other", "hello world")
	os.Chtimes(other, info.ModTime(), info.ModTime())
	os.Rename(other, src)
	if !s.Changed(identify) {
		t.Fatal("a replaced file of equal size and time was missed")
	}
	s, _ = Inspect(src, identify)
	os.Remove(src)
	if !s.Changed(identify) {
		t.Fatal("a removed file was missed")
	}
}

func TestTargetsThatAreTheSourceAreRefused(t *testing.T) {
	dir := realDir(t)
	src := write(t, dir, "a.docx", "x")
	other := write(t, dir, "b.docx", "x")
	os.Link(src, filepath.Join(dir, "hard.docx"))
	os.Symlink(src, filepath.Join(dir, "sym.docx"))
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	os.Symlink(dir, filepath.Join(dir, "sub", "up"))
	s, _ := Inspect(src, identify)
	for name, target := range map[string]string{
		"same path": src, "hard link": filepath.Join(dir, "hard.docx"), "symlink": filepath.Join(dir, "sym.docx"),
		"through a symlinked directory": filepath.Join(dir, "sub", "up", "a.docx"), "dot-dot spelling": filepath.Join(dir, "sub", "..", "a.docx"),
	} {
		if err := CheckTarget(s, target, identify); !errors.Is(err, ErrTargetAlias) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a case variant is the source on a case-insensitive volume (found by identity) and a new file on a case-sensitive one
	variant := filepath.Join(dir, "A.DOCX")
	if vid, err := identify(variant); err == nil {
		if want := vid.Same(s.ID); want != (CheckTarget(s, variant, identify) != nil) {
			t.Errorf("case variant: same file=%v but refused=%v", want, CheckTarget(s, variant, identify) != nil)
		}
	} else if err := CheckTarget(s, variant, identify); err != nil {
		t.Errorf("a case variant that does not exist (case-sensitive volume) was refused: %v", err)
	}
	for name, target := range map[string]string{"another file": other, "a new file": filepath.Join(dir, "new.docx"), "new file via symlinked dir": filepath.Join(dir, "sub", "up", "new.docx")} {
		if err := CheckTarget(s, target, identify); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, target := range map[string]string{"relative": "a.docx", "empty": "", "parent missing": filepath.Join(dir, "missing", "x.docx")} {
		if err := CheckTarget(s, target, identify); !errors.Is(err, ErrSource) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

var accepted = map[string]bool{"docx": true, "pdf": true}

func TestDecideCoversEveryEncryptionFormatAndLimitCase(t *testing.T) {
	l := DefaultLimits()
	cases := []struct {
		name  string
		p     Probe
		state State
		why   string
		note  string
	}{
		{"plain", Probe{Format: "docx", Encryption: None, Pages: 3}, Preflighted, "", ""},
		{"password", Probe{Format: "docx", Encryption: PasswordRequired}, Locked, "", NoteOutputPlain},
		{"permission password only", Probe{Format: "pdf", Encryption: PermissionOnly, Pages: 2}, Preflighted, "", NoteEncryptedOnly},
		{"unsupported encryption", Probe{Format: "pdf", Encryption: Unsupported}, Unprocessable, ReasonEncryption, ""},
		{"format not accepted", Probe{Format: "exe", Encryption: None}, Unprocessable, ReasonFormat, ""},
		{"pages at the limit", Probe{Format: "pdf", Encryption: None, Pages: 2000}, Preflighted, "", ""},
		{"pages over the limit", Probe{Format: "pdf", Encryption: None, Pages: 2001}, Unprocessable, ReasonPages, ""},
		{"bytes at the limit", Probe{Format: "docx", Encryption: None, Bytes: 8 << 30}, Preflighted, "", ""},
		{"bytes over the limit", Probe{Format: "docx", Encryption: None, Bytes: 8<<30 + 1}, Unprocessable, ReasonBytes, ""},
		{"locked and too big is still too big", Probe{Format: "pdf", Encryption: PasswordRequired, Pages: 9999}, Unprocessable, ReasonPages, ""},
	}
	for _, c := range cases {
		o, err := Decide(c.p, accepted, l)
		if err != nil || o.State != c.state || o.Reason != c.why {
			t.Errorf("%s: %+v %v", c.name, o, err)
			continue
		}
		if c.note != "" && (len(o.Notes) != 1 || o.Notes[0] != c.note) {
			t.Errorf("%s: notes %v", c.name, o.Notes)
		}
	}
	o, _ := Decide(Probe{Format: "pdf", Encryption: None, Pages: 2500}, accepted, l)
	if o.Actual != 2500 || o.Limit != 2000 {
		t.Fatalf("the user must be told the actual value and the limit: %+v", o)
	}
}

func TestDecideRefusesAnInvalidProbeInsteadOfGuessing(t *testing.T) {
	for name, p := range map[string]Probe{
		"no format": {Encryption: None}, "unknown encryption": {Format: "docx", Encryption: "rot13"}, "empty encryption": {Format: "docx"},
		"negative pages": {Format: "docx", Encryption: None, Pages: -1}, "negative bytes": {Format: "docx", Encryption: None, Bytes: -1},
		"negative images": {Format: "docx", Encryption: None, Images: -1},
	} {
		if _, err := Decide(p, accepted, DefaultLimits()); !errors.Is(err, ErrProbe) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

type unlocker struct {
	seen  []byte
	hold  []byte // the slice the unlocker was handed, to inspect it afterwards
	err   error
	ref   string
	panic bool
}

func (u *unlocker) Unlock(_ context.Context, _ string, pw []byte) (string, error) {
	u.hold = pw
	u.seen = append([]byte(nil), pw...)
	if u.panic {
		panic("plugin crashed with " + string(pw))
	}
	return u.ref, u.err
}

func zeroed(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return len(b) > 0
}

func TestThePasswordIsZeroedOnEveryPathAndErrorsNeverQuoteIt(t *testing.T) {
	for name, u := range map[string]*unlocker{
		"success": {ref: "res_copy"}, "wrong password": {err: ErrPasswordWrong},
		"failure quoting the password": {err: errors.New("cannot open with hunter2")}, "empty reference": {ref: ""},
	} {
		pw := []byte("hunter2")
		ref, err := Unlock(context.Background(), u, "in", pw)
		if !zeroed(pw) || !zeroed(u.hold) {
			t.Errorf("%s: the password survived", name)
		}
		if string(u.seen) != "hunter2" {
			t.Errorf("%s: the unlocker did not receive the password", name)
		}
		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error quotes the password: %v", name, err)
		}
		switch name {
		case "success":
			if err != nil || ref != "res_copy" {
				t.Errorf("%v %q", err, ref)
			}
		case "wrong password":
			if !errors.Is(err, ErrPasswordWrong) || ref != "" {
				t.Errorf("%v %q", err, ref)
			}
		default:
			if !errors.Is(err, ErrUnlock) || errors.Is(err, ErrPasswordWrong) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	pw := []byte("hunter2")
	u := &unlocker{panic: true}
	func() {
		defer func() { recover() }()
		Unlock(context.Background(), u, "in", pw)
	}()
	if !zeroed(pw) {
		t.Fatal("the password survived a panic in the unlocker")
	}
}

func TestUnlockRefusesIncompleteRequestsAndStillClearsThePassword(t *testing.T) {
	for name, call := range map[string]func(pw []byte) error{
		"no unlocker": func(pw []byte) error { _, err := Unlock(context.Background(), nil, "in", pw); return err },
		"no input":    func(pw []byte) error { _, err := Unlock(context.Background(), &unlocker{ref: "r"}, "", pw); return err },
	} {
		pw := []byte("secret")
		if err := call(pw); !errors.Is(err, ErrUnlock) || !zeroed(pw) {
			t.Errorf("%s: %v zeroed=%v", name, err, zeroed(pw))
		}
	}
	if _, err := Unlock(context.Background(), &unlocker{ref: "r"}, "in", nil); !errors.Is(err, ErrUnlock) {
		t.Error("an empty password was sent")
	}
}

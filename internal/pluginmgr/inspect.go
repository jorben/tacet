package pluginmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrSource    = errors.New("PLUGIN_SOURCE_INVALID")
	ErrDigest    = errors.New("PLUGIN_DIGEST_MISMATCH")
	ErrCommit    = errors.New("PLUGIN_COMMIT_NOT_PINNED")
	ErrBuildCode = errors.New("PLUGIN_BUILD_OR_INSTALL_CODE")
	ErrLock      = errors.New("PLUGIN_LOCK_MISSING_OR_UNSAFE")
	ErrTooLarge  = errors.New("PLUGIN_PACKAGE_TOO_LARGE")
	ErrLayout    = errors.New("PLUGIN_LAYOUT_INVALID")
)

var (
	maxFiles int64 = 20000
	maxTotal int64 = 2 << 30
)

var commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// forbidden names are code that would run at install or build time (SEC-14).
var forbidden = map[string]bool{"setup.py": true, "setup.cfg": true, "install.sh": true, "install.bat": true, "install.ps1": true, "postinstall": true, "preinstall": true, "makefile": true}

// copyTree copies a plugin package into dst as an immutable snapshot. Symbolic
// links, devices and anything that could name a place outside the tree are refused
// instead of followed; the snapshot is made read-only.
func copyTree(src, dst string) error {
	var files, total int64
	src = filepath.Clean(src)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrSource
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) {
			return ErrSource
		}
		if strings.HasPrefix(rel, ".git"+string(filepath.Separator)) || rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		files++
		if files > maxFiles {
			return ErrTooLarge
		}
		in, err := os.Open(path)
		if err != nil {
			return ErrSource
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return ErrSource
		}
		n, err := io.Copy(out, io.LimitReader(in, maxTotal-total+1))
		total += n
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return ErrSource
		}
		if total > maxTotal {
			return ErrTooLarge
		}
		return nil
	})
	return err
}

// treeDigest is a content address of a package: every file's path and bytes, in
// path order. Modes and times do not take part, so the same package gives the same
// digest wherever it was copied from.
func treeDigest(root string) (string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrSource
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return ErrSource
		}
		rel, _ := filepath.Rel(root, path)
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(n)))
		if err != nil {
			return "", ErrSource
		}
		inner := sha256.New()
		size, err := io.Copy(inner, f)
		f.Close()
		if err != nil {
			return "", ErrSource
		}
		fmt.Fprintf(h, "%s\x00%d\x00%x\n", n, size, inner.Sum(nil))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Lock is what the inspection learns from uv.lock.
type Lock struct {
	Packages     int
	NeedsNetwork bool // some wheel has to be downloaded
}

// checkLock accepts only a lock whose every package is a registry package with
// wheels (no source distribution to build) or the plugin itself at the package
// root. Anything else would make the deployment run build code.
func checkLock(data string) (Lock, error) {
	var l Lock
	blocks := strings.Split(data, "[[package]]")
	for _, b := range blocks[1:] {
		l.Packages++
		src := ""
		hasWheels := strings.Contains(b, "wheels = [")
		for _, line := range strings.Split(b, "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "source = ") {
				src = t
			}
		}
		switch {
		case strings.Contains(src, `registry = "`):
			if !hasWheels {
				return Lock{}, ErrLock // only a source distribution: it would be built
			}
			l.NeedsNetwork = true
		case src == `source = { editable = "." }` || src == `source = { virtual = "." }`:
		default:
			return Lock{}, ErrLock
		}
	}
	if l.Packages == 0 {
		return Lock{}, ErrLock
	}
	return l, nil
}

// checkLayout looks for code that runs at install time and for files that would be
// executed or imported implicitly.
func checkLayout(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrSource
		}
		name := strings.ToLower(d.Name())
		if forbidden[name] {
			return ErrBuildCode
		}
		if !d.IsDir() && (strings.HasSuffix(name, ".pth") || strings.HasSuffix(name, ".whl") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".sdist")) {
			return ErrBuildCode
		}
		return nil
	})
}

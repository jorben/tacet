package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var (
	ErrManifest = errors.New("MODEL_MANIFEST_INVALID")
	ErrUnknown  = errors.New("MODEL_UNKNOWN")
)

const (
	maxFiles     = 64
	maxModelSize = 8 << 30
)

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,31}$`)
	wordPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// File is one file of a model package as the signed integrity manifest lists it.
type File struct {
	Name   string
	SHA256 string
	Size   int64
}

// Entry is one model of the catalog. Digest covers the backend and every file's
// name, size and hash, so a task that freezes it can tell a different package from
// the same ID and version (INV-03).
type Entry struct {
	ID, Version, Variant, Backend, License string
	Files                                  []File // sorted by name
	Digest                                 string
}

// Key names the entry in state records and on disk; it contains only safe characters.
func (e Entry) Key() string { return e.ID + "_" + e.Version + "_" + e.Digest[len("sha256:"):][:16] }

// Total is the size of the whole package.
func (e Entry) Total() (n int64) {
	for _, f := range e.Files {
		n += f.Size
	}
	return n
}

type manifestFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifestDoc struct {
	SchemaVersion int                     `json:"schema_version"`
	ID            string                  `json:"id"`
	Version       string                  `json:"version"`
	Variant       string                  `json:"variant"`
	Backend       string                  `json:"backend"`
	License       string                  `json:"license"`
	Files         map[string]manifestFile `json:"files"`
}

// ParseManifest reads one model's integrity manifest. Anything it does not
// recognise is refused: the catalog is what the application trusts to decide what
// may be loaded, so it is parsed strictly.
func ParseManifest(data []byte) (Entry, error) {
	var d manifestDoc
	if err := json.Unmarshal(data, &d); err != nil || d.SchemaVersion != 1 {
		return Entry{}, ErrManifest
	}
	if !idPattern.MatchString(d.ID) || !versionPattern.MatchString(d.Version) || !wordPattern.MatchString(d.Backend) || d.License == "" ||
		(d.Variant != "" && !wordPattern.MatchString(d.Variant)) || len(d.Files) == 0 || len(d.Files) > maxFiles {
		return Entry{}, ErrManifest
	}
	e := Entry{ID: d.ID, Version: d.Version, Variant: d.Variant, Backend: d.Backend, License: d.License}
	var total int64
	for name, f := range d.Files {
		if !namePattern.MatchString(name) || !shaPattern.MatchString(f.SHA256) || f.Size <= 0 || f.Size > maxModelSize {
			return Entry{}, ErrManifest
		}
		total += f.Size
		e.Files = append(e.Files, File{name, f.SHA256, f.Size})
	}
	if total > maxModelSize {
		return Entry{}, ErrManifest
	}
	sort.Slice(e.Files, func(i, j int) bool { return e.Files[i].Name < e.Files[j].Name })
	h := sha256.New()
	fmt.Fprintf(h, "tacet/model/v1\x00%s\x00%s\x00%s\x00%s\n", e.ID, e.Version, e.Variant, e.Backend)
	for _, f := range e.Files {
		fmt.Fprintf(h, "%s\x00%s\x00%d\n", f.Name, f.SHA256, f.Size)
	}
	e.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return e, nil
}

// ParseSigned reads a model manifest only after the application's signature over it
// verified (SEC-13): the manifest decides what may be loaded, so an unsigned or
// altered one is never interpreted. verify is update.Verifier.Verify bound to the
// models purpose by the wiring.
func ParseSigned(data, signature []byte, verify func(payload, signature []byte) error) (Entry, error) {
	if verify == nil || verify(data, signature) != nil {
		return Entry{}, ErrManifestSignature
	}
	return ParseManifest(data)
}

var ErrManifestSignature = errors.New("MODEL_MANIFEST_SIGNATURE_INVALID")

package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

var (
	ErrSignature = errors.New("UPDATE_SIGNATURE_INVALID")
	ErrManifest  = errors.New("UPDATE_MANIFEST_INVALID")
)

// Purpose binds a signature to what it signs, so a signature made for one kind
// of document cannot be replayed as another.
type Purpose string

const (
	PurposeRelease Purpose = "release"
	PurposeModels  Purpose = "models"
)

// Verifier checks detached ed25519 signatures against a set of trusted keys
// (several, so a key can be rotated; a retired key is simply removed).
type Verifier struct{ keys map[string]ed25519.PublicKey }

func NewVerifier(keys map[string]ed25519.PublicKey) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, ErrSignature
	}
	v := &Verifier{keys: map[string]ed25519.PublicKey{}}
	for id, k := range keys {
		if id == "" || len(k) != ed25519.PublicKeySize {
			return nil, ErrSignature
		}
		v.keys[id] = append(ed25519.PublicKey(nil), k...)
	}
	return v, nil
}

// Signature is the detached signature file: which key and the signature, base64.
type Signature struct {
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"`
}

func message(p Purpose, payload []byte) []byte {
	return append([]byte("tacet-sign/v1\x00"+string(p)+"\x00"), payload...)
}

// Sign is for the build and for tests; the application only verifies.
func Sign(key ed25519.PrivateKey, keyID string, p Purpose, payload []byte) []byte {
	b, _ := json.Marshal(Signature{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, message(p, payload)))})
	return b
}

// Verify accepts the payload only if the signature file is well formed, names a
// trusted key and verifies for this purpose. Every failure is the same error.
func (v *Verifier) Verify(p Purpose, payload, signature []byte) error {
	if v == nil || len(payload) == 0 || len(signature) > 4096 {
		return ErrSignature
	}
	var s Signature
	dec := json.NewDecoder(bytesReader(signature))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil || dec.More() {
		return ErrSignature
	}
	key, ok := v.keys[s.KeyID]
	raw, err := base64.StdEncoding.Strict().DecodeString(s.Sig)
	if !ok || err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(key, message(p, payload), raw) {
		return ErrSignature
	}
	return nil
}

var (
	versionRe  = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
	platformRe = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	channelRe  = regexp.MustCompile(`^[a-z]{1,16}$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	shaRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Release is a signed release description. The package digest and size come from
// the signed document, so a package is accepted only if it is the one described.
type Release struct {
	Version  string
	Platform string
	Channel  string
	Name     string
	Size     int64
	SHA256   string
	// PythonABI is the interpreter ABI the release ships; a change makes third-party
	// environments need redeployment (architecture 7.3).
	PythonABI string
	// MinFrom is the oldest version that may update straight to this release ("" = any).
	MinFrom string
}

type releaseDoc struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Channel       string `json:"channel"`
	PythonABI     string `json:"python_abi"`
	MinFrom       string `json:"min_from"`
	Package       struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"package"`
}

// MaxPackage bounds an update package.
const MaxPackage = 4 << 30

// ParseRelease reads a release description strictly.
func ParseRelease(data []byte) (Release, error) {
	var d releaseDoc
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || dec.More() || d.SchemaVersion != 1 {
		return Release{}, ErrManifest
	}
	if !versionRe.MatchString(d.Version) || !platformRe.MatchString(d.Platform) || !channelRe.MatchString(d.Channel) ||
		(d.MinFrom != "" && !versionRe.MatchString(d.MinFrom)) || !nameRe.MatchString(d.Package.Name) ||
		!shaRe.MatchString(d.Package.SHA256) || d.Package.Size <= 0 || d.Package.Size > MaxPackage || len(d.PythonABI) > 32 {
		return Release{}, ErrManifest
	}
	return Release{Version: d.Version, Platform: d.Platform, Channel: d.Channel, Name: d.Package.Name, Size: d.Package.Size, SHA256: d.Package.SHA256,
		PythonABI: d.PythonABI, MinFrom: d.MinFrom}, nil
}

// Newer reports whether a is a later version than b (both well-formed).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) (out [3]int) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return
	}
	for i := 0; i < 3; i++ {
		n := 0
		for _, c := range m[i+1] {
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return
}

func digestHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

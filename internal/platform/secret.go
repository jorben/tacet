package platform

import "errors"

var (
	ErrSecretNotFound    = errors.New("SECRET_NOT_FOUND")
	ErrSecretExists      = errors.New("SECRET_EXISTS")
	ErrSecretUnavailable = errors.New("SECRET_UNAVAILABLE")
)

// Secret is one small secret held by the OS credential store (macOS Keychain,
// Windows DPAPI) under a service and account name. It never falls back to a
// file or to plaintext: when the store is unusable every call returns
// ErrSecretUnavailable. Save refuses to replace an existing secret, because
// replacing a key would orphan everything encrypted under it.
type Secret struct {
	service, account string
}

// NewSecret names a secret. Both names must be non-empty printable ASCII
// without path separators or NUL, since Windows uses them in a file name.
func NewSecret(service, account string) (*Secret, error) {
	for _, s := range []string{service, account} {
		if s == "" || len(s) > 128 {
			return nil, ErrSecretUnavailable
		}
		for _, c := range s {
			if c < 0x21 || c > 0x7e || c == '/' || c == '\\' || c == ':' {
				return nil, ErrSecretUnavailable
			}
		}
	}
	return &Secret{service: service, account: account}, nil
}

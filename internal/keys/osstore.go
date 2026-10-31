package keys

import (
	"errors"

	"github.com/jorben/tacet/internal/platform"
)

// OSUserKeyStore keeps the per-OS-user key in the platform credential store.
// Save never replaces an existing key (ErrKeyExists): a replaced key would
// leave every wrapped key and database unreadable.
type OSUserKeyStore struct{ secret *platform.Secret }

func NewOSUserKeyStore(service, account string) (*OSUserKeyStore, error) {
	s, err := platform.NewSecret(service, account)
	if err != nil {
		return nil, ErrKeystoreUnavailable
	}
	return &OSUserKeyStore{secret: s}, nil
}

func mapSecret(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platform.ErrSecretNotFound):
		return ErrKeyNotFound
	case errors.Is(err, platform.ErrSecretExists):
		return ErrKeyExists
	}
	return ErrKeystoreUnavailable
}

func (o *OSUserKeyStore) Load() ([]byte, error) {
	v, err := o.secret.Load()
	return v, mapSecret(err)
}

func (o *OSUserKeyStore) Save(key []byte) error { return mapSecret(o.secret.Save(key)) }
func (o *OSUserKeyStore) Delete() error         { return mapSecret(o.secret.Delete()) }

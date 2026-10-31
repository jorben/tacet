//go:build !darwin && !windows

package platform

func (*Secret) Save([]byte) error     { return ErrSecretUnavailable }
func (*Secret) Load() ([]byte, error) { return nil, ErrSecretUnavailable }
func (*Secret) Delete() error         { return ErrSecretUnavailable }

//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the secret is protected with DPAPI (bound to the OS user) and the
// ciphertext is stored under %LOCALAPPDATA%. The service and account names are
// also mixed in as entropy, so a blob copied under another name does not open.
// Not yet executed on a real Windows host.

func (s *Secret) path() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" || !filepath.IsAbs(base) {
		return "", ErrSecretUnavailable
	}
	return filepath.Join(base, "Tacet", "secrets", s.service, s.account+".dpapi"), nil
}

func (s *Secret) entropy() *windows.DataBlob {
	b := []byte(s.service + "\x00" + s.account)
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func blobBytes(b windows.DataBlob) []byte {
	return append([]byte(nil), unsafe.Slice(b.Data, b.Size)...)
}

func (s *Secret) Save(value []byte) error {
	if len(value) == 0 {
		return ErrSecretUnavailable
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	in := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	var out windows.DataBlob
	if err = windows.CryptProtectData(&in, nil, s.entropy(), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return ErrSecretUnavailable
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ErrSecretUnavailable
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return ErrSecretExists
	}
	if err != nil {
		return ErrSecretUnavailable
	}
	_, werr := file.Write(blobBytes(out))
	if cerr := file.Close(); werr != nil || cerr != nil {
		os.Remove(path) // never leave a truncated key blob behind
		return ErrSecretUnavailable
	}
	return nil
}

func (s *Secret) Load() ([]byte, error) {
	path, err := s.path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrSecretNotFound
	}
	if err != nil || len(data) == 0 {
		return nil, ErrSecretUnavailable
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err = windows.CryptUnprotectData(&in, nil, s.entropy(), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, ErrSecretUnavailable
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	value := blobBytes(out)
	clear(unsafe.Slice(out.Data, out.Size))
	return value, nil
}

func (s *Secret) Delete() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrSecretUnavailable
	}
	return nil
}

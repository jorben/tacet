package keys

import (
	"errors"
	"os"
	"path/filepath"
)

// DirWrapped keeps wrapped key blobs as files in one directory next to the data
// they protect. A blob is useless without the user key. Save never replaces an
// existing blob (ErrKeyExists) and writes through a temporary file and rename,
// so a crash leaves either no blob or a complete one.
type DirWrapped struct{ dir string }

func NewDirWrapped(dir string) (*DirWrapped, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrKeystoreUnavailable
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, ErrKeystoreUnavailable
	}
	return &DirWrapped{dir: dir}, nil
}

// name rejects ids that could leave the directory or collide across kinds.
func (d *DirWrapped) name(kind Kind, id string) (string, error) {
	if !valid(kind, id) {
		return "", ErrKeyInvalid
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", ErrKeyInvalid
		}
	}
	return filepath.Join(d.dir, string(kind)+"."+id+".key"), nil
}

func (d *DirWrapped) Load(kind Kind, id string) ([]byte, error) {
	path, err := d.name(kind, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, ErrKeystoreUnavailable
	}
	return b, nil
}

func (d *DirWrapped) Save(kind Kind, id string, blob []byte) error {
	path, err := d.name(kind, id)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err == nil {
		return ErrKeyExists
	}
	tmp, err := os.CreateTemp(d.dir, ".wrap-*")
	if err != nil {
		return ErrKeystoreUnavailable
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(blob); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err != nil || cerr != nil {
		return ErrKeystoreUnavailable
	}
	// link fails if the target appeared meanwhile: never replace an existing key
	if err = os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrKeyExists
		}
		return ErrKeystoreUnavailable
	}
	return nil
}

func (d *DirWrapped) Delete(kind Kind, id string) error {
	path, err := d.name(kind, id)
	if err != nil {
		return err
	}
	if err = os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return ErrKeyNotFound
	} else if err != nil {
		return ErrKeystoreUnavailable
	}
	return nil
}

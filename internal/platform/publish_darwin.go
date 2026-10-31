//go:build darwin

package platform

import (
	"errors"

	"golang.org/x/sys/unix"
)

// PublishExclusive atomically renames an already-fsynced target-volume private
// temporary file, never replacing any existing destination. The publish module
// owns authorization, fingerprint/digest checks and the serial commit section.
// Unsupported volumes return a limitation; this API never silently falls back
// to replacing rename or to a copy advertised as atomic.
func PublishExclusive(temporary, destination string) error {
	err := unix.RenamexNp(temporary, destination, unix.RENAME_EXCL)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
		return ErrPublishExists
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
		return ErrPublishUnavailable
	}
	return ErrPublishIO
}

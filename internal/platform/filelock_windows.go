//go:build windows

package platform

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// LockExclusive locks the workspace header's first byte by kernel file identity.
// Keep the file handle open for the complete writable workspace session.
func LockExclusive(file *os.File) error {
	if file == nil {
		return ErrFileLockInvalid
	}
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrFileLockBusy
	}
	if err != nil {
		return ErrFileLockInvalid
	}
	return nil
}

func UnlockExclusive(file *os.File) error {
	if file == nil {
		return ErrFileLockInvalid
	}
	var overlapped windows.Overlapped
	if err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped); err != nil {
		return ErrFileLockInvalid
	}
	return nil
}

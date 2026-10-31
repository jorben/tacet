//go:build darwin

package platform

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// LockExclusive acquires an immediate advisory OS lock on an already-open
// workspace header. Its kernel identity, rather than the spelling of its path,
// controls ownership. The caller keeps the descriptor open until release.
func LockExclusive(file *os.File) error {
	if file == nil {
		return ErrFileLockInvalid
	}
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
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
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return ErrFileLockInvalid
	}
	return nil
}

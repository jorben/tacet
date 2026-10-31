//go:build !darwin && !windows

package platform

import "os"

// Other platforms are outside the MVP platform baseline. Fail explicitly;
// never substitute a PID or lock-file-existence check for kernel ownership.
func LockExclusive(file *os.File) error   { return ErrFileLockInvalid }
func UnlockExclusive(file *os.File) error { return ErrFileLockInvalid }

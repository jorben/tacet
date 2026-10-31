//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows"
)

// PublishExclusive moves within the target volume without REPLACE_EXISTING or
// COPY_ALLOWED. No overwrite or cross-volume fallback is silently introduced.
func PublishExclusive(temporary, destination string) error {
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return ErrPublishIO
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return ErrPublishIO
	}
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return ErrPublishExists
	}
	if errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return ErrPublishUnavailable
	}
	return ErrPublishIO
}

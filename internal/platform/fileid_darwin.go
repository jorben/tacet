//go:build darwin

package platform

import (
	"os"
	"syscall"
)

// Identify follows symlinks and returns the identity of what the path names.
func Identify(path string) (FileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileID{}, ErrFileID
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, ErrFileID
	}
	return FileID{Volume: uint64(uint32(st.Dev)), Index: st.Ino, Links: uint64(st.Nlink)}, nil
}

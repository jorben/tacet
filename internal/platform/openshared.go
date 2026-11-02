//go:build !windows

package platform

import "os"

// OpenShared opens a file like os.OpenFile, but never stops the file from
// being removed or renamed while it is open. That is always so on POSIX
// systems; on Windows the file is opened with FILE_SHARE_DELETE.
func OpenShared(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

//go:build windows

package platform

import "golang.org/x/sys/windows"

// Identify follows reparse points and returns the identity of what the path
// names. Not yet executed on a real Windows host.
func Identify(path string) (FileID, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FileID{}, ErrFileID
	}
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return FileID{}, ErrFileID
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return FileID{}, ErrFileID
	}
	return FileID{Volume: uint64(info.VolumeSerialNumber), Index: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
		Links: uint64(info.NumberOfLinks)}, nil
}

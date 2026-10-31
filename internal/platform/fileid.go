package platform

import "errors"

var ErrFileID = errors.New("FILE_IDENTITY_UNAVAILABLE")

// FileID is the identity the volume gives a file, independent of how its path
// is spelled: two paths with the same Volume and Index are the same file (a
// hard link, a symlink target, a case variant on a case-insensitive volume).
type FileID struct {
	Volume uint64
	Index  uint64
	Links  uint64 // hard link count
}

// Same reports whether both name one file.
func (a FileID) Same(b FileID) bool { return a.Volume == b.Volume && a.Index == b.Index }

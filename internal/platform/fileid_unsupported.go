//go:build !darwin && !windows

package platform

// Identify fails explicitly outside the MVP platform baseline.
func Identify(string) (FileID, error) { return FileID{}, ErrFileID }

//go:build darwin

package platform

import "golang.org/x/sys/unix"

func physicalMemory() (uint64, error) {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n == 0 {
		return 0, ErrMemoryUnknown
	}
	return n, nil
}

func freeSpace(dir string) (int64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(dir, &s); err != nil {
		return 0, err
	}
	return int64(s.Bavail) * int64(s.Bsize), nil
}

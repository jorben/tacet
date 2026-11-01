//go:build !darwin && !windows

package platform

func physicalMemory() (uint64, error) { return 0, ErrMemoryUnknown }

func freeSpace(string) (int64, error) { return 0, ErrMemoryUnknown }

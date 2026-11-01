package platform

import "errors"

// ErrMemoryUnknown: the platform did not say how much memory the machine has.
var ErrMemoryUnknown = errors.New("MEMORY_UNKNOWN")

// PhysicalMemory is the installed memory of the machine in bytes (on Apple Silicon the unified
// memory the GPU shares).
func PhysicalMemory() (uint64, error) { return physicalMemory() }

// FreeSpace is how many bytes the current user may still write on the volume holding dir.
func FreeSpace(dir string) (int64, error) { return freeSpace(dir) }

//go:build darwin || windows

package platform

import "testing"

func TestPhysicalMemoryIsReported(t *testing.T) {
	n, err := PhysicalMemory()
	if err != nil || n < 1<<30 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestFreeSpaceIsReported(t *testing.T) {
	if n, err := FreeSpace(t.TempDir()); err != nil || n <= 0 {
		t.Fatalf("%d %v", n, err)
	}
}

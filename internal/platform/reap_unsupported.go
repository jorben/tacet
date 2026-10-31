//go:build !darwin && !windows

package platform

// ReapMarked fails explicitly outside the MVP platform baseline.
func ReapMarked(marker string) (int, error) { return 0, ErrProcessTree }

// ReapMarkedPrefix fails explicitly outside the MVP platform baseline.
func ReapMarkedPrefix(prefix string) (int, error) { return 0, ErrProcessTree }

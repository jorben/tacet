//go:build windows

package platform

// ReapMarked has nothing to do on Windows: every worker runs in a Job Object
// with KILL_ON_JOB_CLOSE, so the OS reclaims the whole tree when the owning
// process dies, including after a crash. To be verified on a real Windows host.
func ReapMarkedPrefix(prefix string) (int, error) { return ReapMarked(prefix) }

func ReapMarked(marker string) (int, error) {
	if !ValidMarker(marker) {
		return 0, ErrMarker
	}
	return 0, nil
}

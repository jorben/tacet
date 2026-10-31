package platform

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
)

// MarkerEnv names the environment variable that tags every process of one
// execution generation. Descendants inherit it, so a process that left the
// group by calling setsid is still found by ReapMarked (architecture 10, AR-05).
// This is a recovery aid, not a security boundary: code that clears its own
// environment evades it, which is why the sandbox rules remain separate.
const MarkerEnv = "TACET_TREE"

var ErrMarker = errors.New("PROCESS_MARKER_INVALID")

var markerPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidMarker reports whether marker is safe to use as an environment value
// and as a match key.
func ValidMarker(marker string) bool { return markerPattern.MatchString(marker) }

// Mark adds the generation marker to cmd's environment. A nil Env means the
// command would inherit the parent's environment, so that is preserved.
func Mark(cmd *exec.Cmd, marker string) error {
	if cmd == nil || cmd.Process != nil || !ValidMarker(marker) {
		return ErrMarker
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	kept := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if len(entry) >= len(MarkerEnv)+1 && entry[:len(MarkerEnv)+1] == MarkerEnv+"=" {
			continue // never two values for the key
		}
		kept = append(kept, entry)
	}
	cmd.Env = append(kept, MarkerEnv+"="+marker)
	return nil
}

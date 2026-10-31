//go:build darwin

package platform

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// ReapMarked kills every process of this user whose environment carries the
// marker and returns how many were signalled. It is used at startup for the
// previous generation and after a tree kill for descendants that escaped the
// process group. A process that exits during the scan is not an error.
func ReapMarked(marker string) (int, error) { return reap(marker, false) }

// ReapMarkedPrefix kills every process of this user whose marker starts with prefix. A workspace uses it
// at start-up, when it alone holds the workspace lock and none of its own workers exists yet, to remove
// what an earlier run left behind whatever markers its workers carried.
func ReapMarkedPrefix(prefix string) (int, error) { return reap(prefix, true) }

func reap(marker string, prefix bool) (int, error) {
	if !ValidMarker(marker) {
		return 0, ErrMarker
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return 0, ErrProcessTree
	}
	want := []byte(MarkerEnv + "=" + marker)
	self := os.Getpid()
	killed := 0
	for _, proc := range procs {
		pid := int(proc.Proc.P_pid)
		if pid <= 1 || pid == self {
			continue
		}
		raw, err := unix.SysctlRaw("kern.procargs2", pid)
		if err != nil || !hasEnvEntry(raw, want, prefix) {
			continue
		}
		if err = unix.Kill(pid, unix.SIGKILL); err == nil {
			killed++
		}
	}
	return killed, nil
}

// hasEnvEntry parses the kern.procargs2 layout: int32 argc, the executable
// path, NUL padding, argc argument strings, then environment strings. Only the
// environment part is compared, so an argument that merely looks like the
// marker (for example this very scan's own command line) never matches.
func hasEnvEntry(raw, want []byte, prefix bool) bool {
	if len(raw) < 4 {
		return false
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	} else {
		return false
	}
	rest = bytes.TrimLeft(rest, "\x00")
	for ; argc > 0; argc-- {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			return false
		}
		rest = rest[i+1:]
	}
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			i = len(rest)
		}
		if i == 0 {
			break
		}
		if (prefix && bytes.HasPrefix(rest[:i], want)) || bytes.Equal(rest[:i], want) {
			return true
		}
		if i == len(rest) {
			break
		}
		rest = rest[i+1:]
	}
	return false
}

//go:build darwin

package platform

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
}

func echoOff(f *os.File) (restore func(), err error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, ErrNotTerminal
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err = unix.IoctlSetTermios(fd, unix.TIOCSETA, &quiet); err != nil {
		return nil, ErrNotTerminal
	}
	var once sync.Once
	return func() { once.Do(func() { unix.IoctlSetTermios(fd, unix.TIOCSETA, old) }) }, nil
}

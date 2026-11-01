//go:build windows

package platform

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// IsTerminal reports whether f is an interactive console.
func IsTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

func echoOff(f *os.File) (restore func(), err error) {
	h := windows.Handle(f.Fd())
	var old uint32
	if err = windows.GetConsoleMode(h, &old); err != nil {
		return nil, ErrNotTerminal
	}
	quiet := (old &^ windows.ENABLE_ECHO_INPUT) | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err = windows.SetConsoleMode(h, quiet); err != nil {
		return nil, ErrNotTerminal
	}
	var once sync.Once
	return func() { once.Do(func() { windows.SetConsoleMode(h, old) }) }, nil
}

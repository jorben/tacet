//go:build !darwin && !windows

package platform

import "os"

func IsTerminal(*os.File) bool { return false }

func echoOff(*os.File) (func(), error) { return nil, ErrNotTerminal }

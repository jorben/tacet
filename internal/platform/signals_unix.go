//go:build !windows

package platform

import (
	"os"
	"syscall"
)

// TerminationSignals are the signals that mean "the user wants this run to stop".
func TerminationSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

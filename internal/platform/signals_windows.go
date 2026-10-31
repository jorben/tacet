//go:build windows

package platform

import "os"

// TerminationSignals are the signals that mean "the user wants this run to stop".
// Windows delivers Ctrl-C and console close as os.Interrupt.
func TerminationSignals() []os.Signal { return []os.Signal{os.Interrupt} }

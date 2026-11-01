package platform

import (
	"context"
	"errors"
	"os"
)

// ErrNotTerminal: the file is not an interactive terminal; a password is never read from it.
var ErrNotTerminal = errors.New("TERMINAL_REQUIRED")

// maxPassword bounds what one password read takes.
const maxPassword = 1024

// ReadPassword reads one line from the terminal f with echo off (requirements FR-09: the CLI takes a
// password only this way, never from arguments or the environment). Echo is restored before it returns,
// also when ctx ends first (the user's interrupt). prompt, if set, is shown only once echo is off, so that
// what is typed as soon as it appears (or before) is never echoed. The caller clears the returned bytes.
func ReadPassword(ctx context.Context, f *os.File, prompt func()) ([]byte, error) {
	restore, err := echoOff(f)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		prompt()
	}
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var line []byte
		one := make([]byte, 1)
		for {
			n, err := f.Read(one)
			if n == 1 {
				if one[0] == '\n' || one[0] == '\r' {
					break
				}
				if len(line) >= maxPassword {
					clear(line)
					done <- result{nil, ErrNotTerminal}
					return
				}
				line = append(line, one[0])
			}
			if err != nil {
				clear(line)
				done <- result{nil, err}
				return
			}
		}
		done <- result{line, nil}
	}()
	select {
	case r := <-done:
		restore()
		return r.b, r.err
	case <-ctx.Done():
		restore() // the reader stays blocked on the terminal; the process is about to stop
		return nil, ctx.Err()
	}
}

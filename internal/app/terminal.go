package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jorben/tacet/internal/platform"
)

// StdinTerminal is the CLI's terminal: passwords are read from it with echo off (requirements FR-09 and
// 3.6: an interactive terminal may enter them, never arguments or the environment).
type StdinTerminal struct{}

// Interactive reports whether standard input is a terminal a person types on.
func (StdinTerminal) Interactive() bool { return platform.IsTerminal(os.Stdin) }

// Password shows prompt on standard error and reads one line without echo. The caller clears the bytes.
func (StdinTerminal) Password(ctx context.Context, prompt string) ([]byte, error) {
	pw, err := platform.ReadPassword(ctx, os.Stdin, func() { fmt.Fprint(os.Stderr, prompt) })
	fmt.Fprintln(os.Stderr)
	return pw, err
}

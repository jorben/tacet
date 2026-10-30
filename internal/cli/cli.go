package cli

import (
	"fmt"
	"io"

	"github.com/jorben/tacet/internal/app"
)

// Exit codes of requirements 3.6.
const (
	ExitOK          = 0 // every requested file was published per policy
	ExitUsage       = 1 // usage error, or the batch cannot start
	ExitIncomplete  = 2 // something is awaiting review, blocked, failed, partial or undelivered
	ExitInterrupted = 3 // interrupted by the user
)

var commands = []struct{ name, summary string }{
	{"preflight", "Check files against a policy without processing them"},
	{"run", "Process a file list with a versioned policy and publish grant"},
	{"status", "Show batch status and the per-file report"},
	{"resume", "Resume a batch with its original snapshot"},
	{"retry", "Retry selected members"},
	{"clean", "Clean up task data"},
}

// Main runs the CLI and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return ExitOK
	case "version", "--version":
		fmt.Fprintf(stdout, "%s %s\n", app.Name, app.Version)
		return ExitOK
	}
	for _, c := range commands {
		if c.name == args[0] {
			fmt.Fprintf(stderr, "tacet %s: not implemented yet\n", c.name)
			return ExitUsage
		}
	}
	fmt.Fprintf(stderr, "tacet: unknown command %q\n", args[0])
	usage(stderr)
	return ExitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: tacet <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "  %-10s %s\n", "version", "Print the version")
}

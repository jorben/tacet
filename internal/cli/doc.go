// Package cli implements the command line: preflight, run, status, resume,
// retry and clean. It reads passwords without echo on interactive terminals
// only, prints structured redacted progress and maps results to exit codes.
// Every command maps to an app use case; it never re-implements a judgment
// (AR-10). See module design 4.6 (docs/v1/tacet-module-design.md).
package cli

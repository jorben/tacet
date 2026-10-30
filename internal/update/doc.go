// Package update checks, downloads and verifies application updates through
// netgate and settles pending and recoverable tasks before applying one
// (SEC-15). It reaches the exit order through the Quitter interface injected
// at wiring time and does not import lifecycle, which would close an import
// cycle through app. See module design 4.4 (docs/v1/tacet-module-design.md).
package update

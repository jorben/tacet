// Package workspace takes OS-exclusive ownership of a workspace, keeps the
// execution generation and runs the serial commit section. It does not own
// task state (jobs), migrations (store) or the platform lock implementation
// (platform). See module design 5.1 (docs/v1/tacet-module-design.md).
package workspace

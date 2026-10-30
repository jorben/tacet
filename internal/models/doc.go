// Package models keeps the model catalog from the signed integrity manifest,
// runs the per-model download state machine, verifies files and tracks session
// readiness. It never holds a current model; model choice belongs to run
// presets. See module design 9.5 (docs/v1/tacet-module-design.md).
package models

// Package preflight probes each member read-only before a batch is confirmed:
// path safety, fingerprints, actual format, encryption and limits. It
// summarises the batch (BUX-01) and unlocks encrypted inputs, clearing
// passwords after use (FR-09). See module design 6.1 (docs/v1/tacet-module-
// design.md).
package preflight

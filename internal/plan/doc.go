// Package plan turns dispositions into deterministic redaction plans, computes
// target regions shared with the effect preview (MD-15) and widens operations
// after residuals. Build, Regions and Widen are pure logic; export persists
// plans through this package. See module design 7.4 (docs/v1/tacet-module-
// design.md).
package plan

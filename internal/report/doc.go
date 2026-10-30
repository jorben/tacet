// Package report builds the redacted per-file reports, the delivery manifest
// and the encrypted local locator index. It reads other modules and writes
// files only through publish.ArtifactWriter. See module design 8.5
// (docs/v1/tacet-module-design.md).
package report

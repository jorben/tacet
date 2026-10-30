// Package verdict judges whether a file may be published, its result status
// and the batch summary. Publishing gates, reports, the GUI and the CLI all
// use these judgments instead of deriving their own (INV-01, AR-10). It is
// pure logic. See module design 8.3 (docs/v1/tacet-module-design.md).
package verdict

// Package lineage decides which stage results are stale when an upstream
// version or snapshot facet changes (INV-04). It is pure logic without
// storage; its inputs come from jobs. See module design 5.4 (docs/v1/tacet-
// module-design.md).
package lineage

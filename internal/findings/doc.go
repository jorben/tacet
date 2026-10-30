// Package findings stores findings, maintains grouping keys (including the
// batch-keyed same-value HMAC) and indexes, and serves paged, grouped and
// representative-sample queries. It never stores original values. See module
// design 6.5 (docs/v1/tacet-module-design.md).
package findings

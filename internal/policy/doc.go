// Package policy turns findings, coverage gaps and decisions into dispositions
// and exception drafts under the frozen policy, including the minimum-
// confidence filter (MD-12). Resolve is pure logic; the outer layer is the
// resolve stage and the read-only Simulate. See module design 7.1
// (docs/v1/tacet-module-design.md).
package policy

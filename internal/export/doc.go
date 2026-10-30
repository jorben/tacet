// Package export runs the export stage: it builds or loads the plan, calls the
// format plugin's write, checks that every approved operation is accounted for
// and registers the candidate with its output provenance. See module design
// 8.1 (docs/v1/tacet-module-design.md).
package export

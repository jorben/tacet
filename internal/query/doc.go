// Package query maintains read models from outbox events, serves bounded paged
// queries and pushes throttled summaries (about five per second per
// subscription). It is read-only and takes business judgments such as result
// states from verdict. See module design 4.2 (docs/v1/tacet-module-design.md).
package query

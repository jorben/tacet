// Package retention keeps retention grants and deadlines, purges task data
// with crypto-erase, sweeps expired data at start-up and maintains the task
// history (requirements 7.1). Owners of task data register a Purger; retention
// does not import them. See module design 5.6 (docs/v1/tacet-module-
// design.md).
package retention

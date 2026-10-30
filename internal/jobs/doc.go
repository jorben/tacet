// Package jobs runs the batch and file-task state machines, stage records,
// stage outcome handling, automatic retry budgets, the coexistence of one
// quick task with one batch, and pause, cancel and recovery (MD-08, MD-10). It
// does not admit resources (sched), call plugins (stage modules) or judge
// dispositions and exception actions (policy, exceptions). See module design
// 5.3 (docs/v1/tacet-module-design.md).
package jobs

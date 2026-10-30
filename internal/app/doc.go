// Package app holds the use cases behind user commands: it validates
// parameters and available actions, writes through the workspace commit
// section with version conditions and returns coded errors. Long-running work
// is handed to jobs and sched; app never calls plugins. User-triggered
// orchestration lives here, stage-triggered orchestration lives in jobs
// (MD-05). See module design 4.1 (docs/v1/tacet-module-design.md).
package app

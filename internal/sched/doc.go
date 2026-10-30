// Package sched admits stages against resource budgets, keeps the interactive
// and background priorities (AR-16), applies back-pressure and handles memory
// pressure and the storage high-water mark (AR-12). It knows stages only
// through StageRunner; its state lives in memory and is rebuilt from jobs at
// start-up. See module design 5.5 (docs/v1/tacet-module-design.md).
package sched

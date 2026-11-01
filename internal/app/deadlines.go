package app

import (
	"context"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
)

// stageLimit is the deadline of one plugin call of a stage (requirements 5.4: probe, pages and windows,
// export are given their own): a base plus an allowance per MiB of the input, up to a ceiling. The figures
// are set from the runs on the development machine, not measured on the R1 device; a file whose call
// needs longer fails with STAGE_DEADLINE_EXCEEDED and can be processed again.
type stageLimit struct{ base, perMiB, max time.Duration }

var stageLimits = map[kernel.StageKind]stageLimit{
	kernel.StagePreflight:     {base: time.Minute, perMiB: 2 * time.Second, max: 10 * time.Minute},
	kernel.StageExtractDetect: {base: 2 * time.Minute, perMiB: 20 * time.Second, max: 2 * time.Hour},
	kernel.StageExport:        {base: 2 * time.Minute, perMiB: 30 * time.Second, max: 2 * time.Hour},
	kernel.StageValidate:      {base: 2 * time.Minute, perMiB: 30 * time.Second, max: 2 * time.Hour},
}

// stageDeadline is the deadline of a plugin call of stage kind on an input of size bytes; zero for stages
// that call no plugin (the host's default applies if one does).
func stageDeadline(kind kernel.StageKind, size int64) time.Duration {
	l, ok := stageLimits[kind]
	if !ok {
		return 0
	}
	d := l.base + time.Duration(size>>20)*l.perMiB
	if d > l.max {
		d = l.max
	}
	return d
}

// stageContext is the scheduler's stage context with a deadline budget for the plugin calls under it.
type stageContext struct {
	context.Context
	attempt kernel.AttemptRef
}

func (c stageContext) Attempt() kernel.AttemptRef { return c.attempt }

// withDeadline gives the stage its budget; exceeded reports whether a plugin call ended because of it.
func (e *Engine) withDeadline(ctx sched.StageContext, kind kernel.StageKind) (sched.StageContext, func() bool) {
	var size int64
	e.ws.Commit(ctx, func(tx store.Tx) error {
		if m, err := getMember(tx, ctx.Attempt().JobID); err == nil {
			size = m.Size
		}
		return nil
	})
	deadline := e.deadline
	if deadline == nil {
		deadline = stageDeadline
	}
	d := deadline(kind, size)
	if d <= 0 {
		return ctx, func() bool { return false }
	}
	bctx, exceeded := host.WithBudget(ctx, d)
	return stageContext{Context: bctx, attempt: ctx.Attempt()}, exceeded
}

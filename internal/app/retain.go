package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jorben/tacet/internal/export"
	"github.com/jorben/tacet/internal/jobs"
	"github.com/jorben/tacet/internal/policy"
	"github.com/jorben/tacet/internal/publish"
	"github.com/jorben/tacet/internal/retention"
	"github.com/jorben/tacet/internal/review"
	"github.com/jorben/tacet/internal/sched"
	"github.com/jorben/tacet/internal/store"
	"github.com/jorben/tacet/internal/validate"
)

// buildRetention wires the clean-up of a task's data: every module that keeps rows or files for a job
// registers how to remove them, and the task key is erased first so nothing left behind can be read.
func (e *Engine) buildRetention() error {
	tx := func(f func(store.Tx, string) error) retention.Purger {
		return retention.PurgerFunc(func(_ context.Context, t store.Tx, job string) error { return f(t, job) })
	}
	regs := []retention.Registration{
		{Name: "coverage", Tx: tx(e.cov.Purge)},
		{Name: "exceptions", Tx: tx(e.exc.Purge)},
		{Name: "export", Tx: tx((export.Repository{}).Purge)},
		{Name: "extract", Tx: tx(e.units.Purge)},
		{Name: "findings", Tx: tx(e.fr.Purge)},
		{Name: "policy", Tx: tx(policy.Purge)},
		{Name: "publish", Tx: tx(publish.Purge)},
		{Name: "review", Tx: tx(review.Purge)},
		{Name: "validate", Tx: tx(validate.Purge)},
		{Name: "bodies", Func: func(_ context.Context, job string) error {
			return os.RemoveAll(filepath.Join(e.opt.Dir, "bodies", job))
		}},
		{Name: "previews", Func: func(_ context.Context, job string) error { e.pv.dropJob(job); return nil }},
		{Name: "resources", Func: func(ctx context.Context, job string) error { _, err := e.refs.Purge(ctx, job); return err }},
		// last in name order, so a job is only marked gone once everything else is
		{Name: "zz_app", Func: func(ctx context.Context, job string) error {
			return e.ws.Commit(ctx, func(t store.Tx) error { return markPurged(t, job) })
		}},
	}
	var err error
	e.ret, err = retention.New(retention.Config{
		Commit:    e.ws.Commit,
		EraseKey:  e.ws.Keys().CryptoErase,
		Unpin:     e.reg.Unpin,
		Workspace: hexSum([]byte(e.opt.Dir))[:16],
		Jobs:      e.liveJobs,
		Now:       e.opt.Now,
	}, regs...)
	return err
}

// liveJobs lists the jobs that still have data.
func (e *Engine) liveJobs(ctx context.Context) ([]string, error) {
	var out []string
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		rows, err := t.Query(`SELECT job FROM app_member WHERE purged=0 ORDER BY job`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}

// retentionDays is how long the person's settings keep unfinished work and results (0: not at all).
func (e *Engine) retentionDays(ctx context.Context) int {
	days := 7
	e.ws.Commit(ctx, func(tx store.Tx) error {
		if s, err := e.cfg.Settings(tx); err == nil {
			days = s.RetentionDays
		}
		return nil
	})
	return days
}

// grantRetention records the chosen retention for the jobs of a new batch. With 0 days nothing is
// granted, so the data is removed at the next start.
func (e *Engine) grantRetention(ctx context.Context, jobIDs []string) {
	days := e.retentionDays(ctx)
	if days <= 0 {
		return
	}
	for _, id := range jobIDs {
		e.ret.Grant(ctx, id, days)
	}
}

// applyRetention runs at start-up after interrupted work was paused: work whose retention is over (or
// was never granted) is cancelled, so no batch is left half-alive without its data, and then its data
// is removed.
func (e *Engine) applyRetention(ctx context.Context) error {
	ids, err := e.liveJobs(ctx)
	if err != nil {
		return err
	}
	batches := map[string]bool{}
	for _, id := range ids {
		if ok, _ := e.ret.Usable(ctx, id); ok {
			continue
		}
		var j jobs.Job
		if err = e.ws.Commit(ctx, func(tx store.Tx) (err error) { j, err = e.jobs.Get(tx, id); return }); err != nil {
			continue
		}
		if !j.Position.State.Terminal() {
			if err = e.apply(ctx, id, jobs.Event{Kind: jobs.EvCancel}, true); err == nil {
				err = e.apply(ctx, id, jobs.Event{Kind: jobs.EvCancelDone}, true)
			}
		}
		batches[j.Batch] = true
	}
	for b := range batches {
		e.ws.Commit(ctx, func(tx store.Tx) error { return e.jobs.FinishBatch(tx, b) })
	}
	_, err = e.ret.Sweep(ctx)
	return err
}

// budgetFor is the scheduler budget of a resource tier: the comfortable tier works on one file at a
// time within 2 GiB, the standard tier on two within 4 GiB.
func budgetFor(tier string) sched.Budget {
	b := sched.Budget{MemoryBytes: 2 << 30, ScratchBytes: 4 << 30, CandidateBytes: 4 << 30, QueueBytes: 64 << 20, CPUSlots: 1, MaxRunning: 1, MaxQueued: 100000,
		Workers: map[string]int{"format": 1, "detect": 1}}
	if tier == "std" {
		b.MemoryBytes, b.CPUSlots, b.MaxRunning = 4<<30, 2, 2
		b.Workers = map[string]int{"format": 2, "detect": 1}
	}
	return b
}

func (e *Engine) tier(ctx context.Context) string {
	tier := "comfort"
	e.ws.Commit(ctx, func(tx store.Tx) error {
		if s, err := e.cfg.Settings(tx); err == nil {
			tier = s.ResourceTier
		}
		return nil
	})
	return tier
}

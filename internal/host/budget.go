package host

import (
	"context"
	"sync/atomic"
	"time"
)

type budgetKey struct{}

type budget struct {
	d   time.Duration
	hit atomic.Bool
}

// WithBudget gives the plugin calls made under ctx the deadline d (requirements 5.4: deadlines are set per
// unit of work). Unlike a context deadline it is kept by the host's watch, which counts time awake only, so
// the machine sleeping does not use it up; the plugin is told it as deadline_ms. exceeded reports whether a
// call ended because of it, whether the plugin said so (DEADLINE_EXCEEDED) or the host had to stop it.
func WithBudget(ctx context.Context, d time.Duration) (_ context.Context, exceeded func() bool) {
	b := &budget{d: d}
	return context.WithValue(ctx, budgetKey{}, b), b.hit.Load
}

func budgetOf(ctx context.Context) *budget {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if b == nil || b.d <= 0 {
		return nil
	}
	return b
}

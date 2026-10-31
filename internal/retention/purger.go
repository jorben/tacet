package retention

import (
	"context"

	"github.com/jorben/tacet/internal/store"
)

// Purger deletes the data one module owns for a task. Owners register a
// Purger at wiring time, so retention never imports them.
type Purger interface {
	Purge(ctx context.Context, tx store.Tx, jobID string) error
}

// PurgerFunc adapts a function to Purger, so owners whose own Purge has another
// shape (it takes no context) register without a wrapper type.
type PurgerFunc func(ctx context.Context, tx store.Tx, jobID string) error

func (f PurgerFunc) Purge(ctx context.Context, tx store.Tx, jobID string) error {
	return f(ctx, tx, jobID)
}

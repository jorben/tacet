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

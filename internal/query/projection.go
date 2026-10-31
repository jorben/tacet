package query

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jorben/tacet/internal/store"
)

var ErrProjection = errors.New("QUERY_PROJECTION_INVALID")

// Module owns only the read projection and aggregates. Domain facts are read
// through their owner's interface; query never reads jobs tables directly.
func Module() store.Module {
	return store.Module{Name: "query", Prefix: "query_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE query_job(id TEXT PRIMARY KEY,batch TEXT NOT NULL,version INTEGER NOT NULL,state TEXT NOT NULL,ordinal INTEGER NOT NULL)`,
		`CREATE INDEX query_members ON query_job(batch,ordinal,id)`,
		`CREATE TABLE query_count(batch TEXT NOT NULL,state TEXT NOT NULL,members INTEGER NOT NULL,PRIMARY KEY(batch,state))`,
	}}}}
}

// JobView is the bounded, neutral projection fact returned by the owning module.
// No document text, original values or full paths appear in outbox notifications.
type JobView struct {
	ID, Batch, State string
	Version          uint64
	Ordinal          int
}

type Jobs interface {
	View(store.Tx, string) (JobView, error)
}

type Projection struct {
	db   *store.DB
	jobs Jobs
}

func NewProjection(db *store.DB, jobs Jobs) *Projection { return &Projection{db: db, jobs: jobs} }

// Pump consumes at most limit events atomically with the incremental projection.
// It can be called repeatedly or after restart without double-counting states.
func (p *Projection) Pump(ctx context.Context, limit int) (int, error) {
	if p.db == nil || p.jobs == nil {
		return 0, ErrProjection
	}
	return p.db.Consume(ctx, "query/jobs/v1", limit, func(tx *store.Txn, events []store.Delivered) error {
		scope, err := tx.Module("query")
		if err != nil {
			return err
		}
		for _, delivered := range events {
			event := delivered.Event
			if event.Type != "JobStateChanged" {
				continue
			}
			view, err := p.jobs.View(tx, event.Subject.ID)
			if err != nil {
				return err
			}
			if view.ID != event.Subject.ID || view.Batch == "" || view.State == "" || view.Version < event.Subject.Version {
				return ErrProjection
			}
			var previousVersion uint64
			var previousState, previousBatch string
			err = tx.QueryRow("SELECT batch,state,version FROM query_job WHERE id=?", view.ID).Scan(&previousBatch, &previousState, &previousVersion)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && previousVersion >= view.Version {
				continue
			}
			if previousState != "" {
				if _, err = scope.Exec("UPDATE query_count SET members=members-1 WHERE batch=? AND state=?", previousBatch, previousState); err != nil {
					return err
				}
			}
			if _, err = scope.Exec(`INSERT INTO query_count(batch,state,members) VALUES(?,?,1) ON CONFLICT(batch,state) DO UPDATE SET members=members+1`, view.Batch, view.State); err != nil {
				return err
			}
			if _, err = scope.Exec(`INSERT INTO query_job(id,batch,version,state,ordinal) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET batch=excluded.batch,version=excluded.version,state=excluded.state,ordinal=excluded.ordinal`, view.ID, view.Batch, view.Version, view.State, view.Ordinal); err != nil {
				return err
			}
		}
		return nil
	})
}

// Members returns a bounded stable page, keyed by (ordinal,id). The cursor
// binds the batch externally in app/query DTOs; page size is always capped.
func (p *Projection) Members(ctx context.Context, batch string, afterOrdinal int, afterID string, limit int) ([]JobView, error) {
	if batch == "" || afterOrdinal < -1 || limit <= 0 || limit > 200 {
		return nil, ErrProjection
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,batch,state,version,ordinal FROM query_job WHERE batch=? AND (ordinal>? OR (ordinal=? AND id>?)) ORDER BY ordinal,id LIMIT ?`, batch, afterOrdinal, afterOrdinal, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []JobView
	for rows.Next() {
		var view JobView
		if err = rows.Scan(&view.ID, &view.Batch, &view.State, &view.Version, &view.Ordinal); err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, rows.Err()
}

// Counts reads the small incremental state summary, never recounts findings or
// traverses every file for a progress update. Display labels come from verdict.
func (p *Projection) Counts(ctx context.Context, batch string) (map[string]int, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT state,members FROM query_count WHERE batch=? AND members>0 ORDER BY state", batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err = rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		result[state] = count
	}
	return result, rows.Err()
}

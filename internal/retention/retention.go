package retention

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrInvalid     = errors.New("RETENTION_REQUEST_INVALID")
	ErrPublishing  = errors.New("RETENTION_PUBLISH_IN_PROGRESS")
	ErrPurgeFailed = errors.New("RETENTION_PURGE_INCOMPLETE")
	ErrStore       = errors.New("RETENTION_STORE_FAILED")
)

// MaxDays bounds a grant so the deadline arithmetic cannot overflow.
const MaxDays = 36500

// Registration is one module's cleanup. Exactly one of Tx and Func is set.
// Tx deletes rows inside the workspace commit section (the Purger contract in
// purger.go). Func is for cleanup that does bulk I/O such as resource files and
// manages its own commits, because the commit section must not do file work.
// Either must be idempotent: a retry calls it again after a partial failure.
type Registration struct {
	Name string
	Tx   Purger
	Func func(ctx context.Context, job string) error
}

// Commit is workspace.Commit; retention may not import workspace.
type Commit func(ctx context.Context, fn func(store.Tx) error) error

// Module owns the grant and the visible purge-step tables.
func Module() store.Module {
	return store.Module{Name: "retention", Prefix: "retention_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE retention_grant(owner TEXT PRIMARY KEY, days INTEGER NOT NULL, saved_at INTEGER NOT NULL, expired INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE retention_step(owner TEXT NOT NULL, step TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(owner, step))`,
	}}}}
}

// Config wires the service to the modules it may use (module design 5.6).
type Config struct {
	Commit Commit
	// EraseKey deletes the wrapped task key (keys.Manager.CryptoErase).
	EraseKey func(kind keys.Kind, id string) error
	// Unpin drops the plugin/resource reference pins of an owner (registry.Repository.Unpin).
	Unpin func(tx store.Tx, workspace, owner string) error
	// Workspace is the id registry pins are scoped to.
	Workspace string
	// PublishBusy reports an in-progress publish of the job; purging waits for it.
	PublishBusy func(job string) bool
	// Jobs lists every job that has data in the workspace, for the start-up sweep.
	Jobs func(ctx context.Context) ([]string, error)
	Now  func() time.Time
}

type Service struct {
	cfg     Config
	purgers []Registration
}

func New(cfg Config, purgers ...Registration) (*Service, error) {
	if cfg.Commit == nil || cfg.EraseKey == nil || cfg.Unpin == nil {
		return nil, ErrInvalid
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	seen := map[string]bool{}
	for _, p := range purgers {
		if p.Name == "" || seen[p.Name] || (p.Tx == nil) == (p.Func == nil) {
			return nil, ErrInvalid
		}
		seen[p.Name] = true
	}
	sorted := append([]Registration(nil), purgers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return &Service{cfg: cfg, purgers: sorted}, nil
}

func scope(tx store.Tx) (*store.Scope, *store.Txn, error) {
	c, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := c.Module("retention")
	return sc, c, err
}

// Grant records an explicit user choice to keep a job's data across restarts
// for the given days. It is also the only way to renew: the period runs from
// this call, and nothing in the background extends it.
func (s *Service) Grant(ctx context.Context, owner string, days int) error {
	if owner == "" || days < 1 || days > MaxDays {
		return ErrInvalid
	}
	at := s.cfg.Now().Unix()
	if err := s.cfg.Commit(ctx, func(tx store.Tx) error {
		sc, _, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO retention_grant(owner, days, saved_at, expired) VALUES(?,?,?,0)
			ON CONFLICT(owner) DO UPDATE SET days=excluded.days, saved_at=excluded.saved_at, expired=0`, owner, days, at)
		return err
	}); err != nil {
		return ErrStore
	}
	return nil
}

type grantRow struct {
	days          int
	savedAt       int64
	expiredMarker bool
}

func (g grantRow) deadline() int64 { return g.savedAt + int64(g.days)*86400 }

func (s *Service) load(ctx context.Context, owner string) (g grantRow, ok bool, err error) {
	err = s.cfg.Commit(ctx, func(tx store.Tx) error {
		_, c, e := scope(tx)
		if e != nil {
			return e
		}
		rows, e := c.Query(`SELECT days, saved_at, expired FROM retention_grant WHERE owner=?`, owner)
		if e != nil {
			return e
		}
		defer rows.Close()
		if rows.Next() {
			var flag int
			if e = rows.Scan(&g.days, &g.savedAt, &flag); e != nil {
				return e
			}
			g.expiredMarker, ok = flag != 0, true
		}
		return rows.Err()
	})
	if err != nil {
		return g, false, ErrStore
	}
	return g, ok, nil
}

// Usable reports whether the job's retained data may still be used. A missing
// grant, a marked grant and a passed deadline all answer false, checked against
// the clock at call time so there is no window between expiry and the sweep.
func (s *Service) Usable(ctx context.Context, owner string) (bool, error) {
	g, ok, err := s.load(ctx, owner)
	if err != nil || !ok {
		return false, err
	}
	return !g.expiredMarker && s.cfg.Now().Unix() < g.deadline(), nil
}

func (s *Service) mark(ctx context.Context, owner, step, state string) error {
	if err := s.cfg.Commit(ctx, func(tx store.Tx) error {
		sc, _, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO retention_step(owner, step, state) VALUES(?,?,?)
			ON CONFLICT(owner, step) DO UPDATE SET state=excluded.state`, owner, step, state)
		return err
	}); err != nil {
		return ErrStore
	}
	return nil
}

// Steps returns the visible state of a purge in progress or failed:
// step name -> "done" | "failed".
func (s *Service) Steps(ctx context.Context, owner string) (map[string]string, error) {
	out := map[string]string{}
	err := s.cfg.Commit(ctx, func(tx store.Tx) error {
		_, c, e := scope(tx)
		if e != nil {
			return e
		}
		rows, e := c.Query(`SELECT step, state FROM retention_step WHERE owner=?`, owner)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var step, state string
			if e = rows.Scan(&step, &state); e != nil {
				return e
			}
			out[step] = state
		}
		return rows.Err()
	})
	if err != nil {
		return nil, ErrStore
	}
	return out, nil
}

// Purge removes everything of a finished or stopped job, in the order of the
// design: wait out a publish, erase the task key (everything encrypted under it
// is unreadable from here on), run every registered Purger, drop the registry
// pins. Each step is recorded; steps already done are skipped on a retry and a
// failed one is retried. The caller guarantees the job has stopped. The grant
// and step rows are removed only after every step succeeded, so a purge that
// failed halfway stays visible and is picked up again by Sweep.
func (s *Service) Purge(ctx context.Context, job string) error {
	if job == "" {
		return ErrInvalid
	}
	if s.cfg.PublishBusy != nil && s.cfg.PublishBusy(job) {
		return ErrPublishing
	}
	done, err := s.Steps(ctx, job)
	if err != nil {
		return err
	}
	run := func(step string, fn func() error) error {
		if done[step] == "done" {
			return nil
		}
		if err := fn(); err != nil {
			if merr := s.mark(ctx, job, step, "failed"); merr != nil {
				return merr
			}
			return err
		}
		return s.mark(ctx, job, step, "done")
	}
	if err = run("erase_key", func() error { return s.cfg.EraseKey(keys.Task, job) }); err != nil {
		return ErrPurgeFailed // nothing else is attempted while the key still decrypts the data
	}
	failed := false
	for _, p := range s.purgers {
		p := p
		if err = run("purge:"+p.Name, func() error {
			if p.Func != nil {
				return p.Func(ctx, job)
			}
			return s.cfg.Commit(ctx, func(tx store.Tx) error { return p.Tx.Purge(ctx, tx, job) })
		}); err != nil {
			if errors.Is(err, ErrStore) {
				return err
			}
			failed = true // the others are independent: keep going, report at the end
		}
	}
	if err = run("unpin", func() error {
		return s.cfg.Commit(ctx, func(tx store.Tx) error { return s.cfg.Unpin(tx, s.cfg.Workspace, job) })
	}); err != nil {
		if errors.Is(err, ErrStore) {
			return err
		}
		failed = true
	}
	if failed {
		return ErrPurgeFailed
	}
	if err = s.cfg.Commit(ctx, func(tx store.Tx) error {
		sc, _, e := scope(tx)
		if e != nil {
			return e
		}
		if _, e = sc.Exec(`DELETE FROM retention_step WHERE owner=?`, job); e != nil {
			return e
		}
		_, e = sc.Exec(`DELETE FROM retention_grant WHERE owner=?`, job)
		return e
	}); err != nil {
		return ErrStore
	}
	return nil
}

// SweepReport says what a start-up sweep did.
type SweepReport struct {
	Kept    int
	Purged  []string
	Expired []string // purged because the deadline had passed
	Failed  []string // purge incomplete; retried at the next sweep
	Busy    []string // publish in progress; left for a later sweep
}

// Sweep settles every job with data in the workspace at start-up: a job without
// a grant, or with an expired one, is purged. Expired grants are marked first,
// so use is blocked even if the purge then fails. Jobs with a purge in progress
// (steps recorded) are resumed.
func (s *Service) Sweep(ctx context.Context) (SweepReport, error) {
	var r SweepReport
	if s.cfg.Jobs == nil {
		return r, ErrInvalid
	}
	jobs, err := s.cfg.Jobs(ctx)
	if err != nil {
		return r, ErrStore
	}
	sort.Strings(jobs)
	for _, job := range jobs {
		g, granted, err := s.load(ctx, job)
		if err != nil {
			return r, err
		}
		steps, err := s.Steps(ctx, job)
		if err != nil {
			return r, err
		}
		expired := granted && (g.expiredMarker || s.cfg.Now().Unix() >= g.deadline())
		if granted && !expired && len(steps) == 0 {
			r.Kept++
			continue
		}
		if expired && !g.expiredMarker {
			if err = s.cfg.Commit(ctx, func(tx store.Tx) error {
				sc, _, e := scope(tx)
				if e != nil {
					return e
				}
				_, e = sc.Exec(`UPDATE retention_grant SET expired=1 WHERE owner=?`, job)
				return e
			}); err != nil {
				return r, ErrStore
			}
		}
		switch err = s.Purge(ctx, job); {
		case err == nil:
			r.Purged = append(r.Purged, job)
			if expired {
				r.Expired = append(r.Expired, job)
			}
		case errors.Is(err, ErrPublishing):
			r.Busy = append(r.Busy, job)
		case errors.Is(err, ErrStore):
			return r, err
		default:
			r.Failed = append(r.Failed, job)
		}
	}
	return r, nil
}

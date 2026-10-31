package refs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/jorben/tacet/internal/store"
)

var ErrCatalog = errors.New("RESOURCE_CATALOG_FAILED")

// Entry is the persistent fact about one finished resource. Path is relative to
// the resource directory when the file lives there, so the workspace can move.
type Entry struct {
	ID, Job, Path string
	Version       uint64
	Digest        string
	Size          int64
}

// Catalog persists finished resources. Implementations commit durably before
// returning. The manager never calls it while holding its own lock, because the
// workspace commit section may call back into Revoke.
type Catalog interface {
	Put(ctx context.Context, e Entry) error
	Remove(ctx context.Context, id string) error
	List(ctx context.Context) ([]Entry, error)
}

// Commit is workspace.Commit; refs may not import workspace.
type Commit func(ctx context.Context, fn func(store.Tx) error) error

// Module is the store module that owns the catalog table.
func Module() store.Module {
	return store.Module{Name: "refs", Prefix: "refs_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE refs_resource(id TEXT PRIMARY KEY, job TEXT NOT NULL, path TEXT NOT NULL,
			version INTEGER NOT NULL, digest TEXT NOT NULL, size INTEGER NOT NULL)`,
		`CREATE INDEX refs_resource_job ON refs_resource(job)`,
	}}}}
}

type sqlCatalog struct{ commit Commit }

func NewSQLCatalog(commit Commit) Catalog { return sqlCatalog{commit} }

func scope(tx store.Tx) (*store.Scope, *store.Txn, error) {
	c, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := c.Module("refs")
	return sc, c, err
}

func (c sqlCatalog) Put(ctx context.Context, e Entry) error {
	return c.commit(ctx, func(tx store.Tx) error {
		sc, _, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`INSERT INTO refs_resource(id, job, path, version, digest, size) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET job=excluded.job, path=excluded.path, version=excluded.version,
			digest=excluded.digest, size=excluded.size`, e.ID, e.Job, e.Path, e.Version, e.Digest, e.Size)
		return err
	})
}

func (c sqlCatalog) Remove(ctx context.Context, id string) error {
	return c.commit(ctx, func(tx store.Tx) error {
		sc, _, err := scope(tx)
		if err != nil {
			return err
		}
		_, err = sc.Exec(`DELETE FROM refs_resource WHERE id=?`, id)
		return err
	})
}

func (c sqlCatalog) List(ctx context.Context) ([]Entry, error) {
	var out []Entry
	err := c.commit(ctx, func(tx store.Tx) error {
		_, concrete, err := scope(tx)
		if err != nil {
			return err
		}
		rows, err := concrete.Query(`SELECT id, job, path, version, digest, size FROM refs_resource ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err = rows.Scan(&e.ID, &e.Job, &e.Path, &e.Version, &e.Digest, &e.Size); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// UseCatalog attaches persistence. Call it before any resource is created.
func (m *Manager) UseCatalog(c Catalog) {
	m.mu.Lock()
	m.catalog = c
	m.mu.Unlock()
}

// SetJobQuota caps, per job, the finished bytes plus the limits of writes in
// progress. 0 means unlimited.
func (m *Manager) SetJobQuota(bytes int64) {
	m.mu.Lock()
	m.quota = bytes
	m.mu.Unlock()
}

// used must be called with m.mu held.
func (m *Manager) used(job string) (n int64) {
	for _, r := range m.res {
		if r.job != job {
			continue
		}
		switch r.st {
		case open:
			n += r.limit
		case frozen, sealing:
			n += r.size
		}
	}
	return n
}

func (m *Manager) entry(r *record) Entry {
	path := r.path
	if rel, err := filepath.Rel(m.dir, r.path); err == nil && rel != "." && !filepath.IsAbs(rel) && filepath.Base(rel) == rel {
		path = rel
	}
	return Entry{ID: r.id, Job: r.job, Path: path, Version: r.version, Digest: r.digest, Size: r.size}
}

// publish writes a sealed record to the catalog outside refs.mu and only then
// makes it readable. If the catalog refuses, the resource and its file are gone:
// something unreadable after a restart must not look finished now.
func (m *Manager) publish(r *record) error {
	m.mu.Lock()
	catalog, e := m.catalog, m.entry(r)
	m.mu.Unlock()
	var err error
	if catalog != nil {
		err = catalog.Put(context.Background(), e)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		r.st = abandoned
		delete(m.res, r.id)
		if filepath.Dir(r.path) == m.dir {
			os.Remove(r.path)
		}
		return ErrCatalog
	}
	r.st = frozen
	return nil
}

// Recovery reports what Recover found.
type Recovery struct {
	Loaded  int
	Missing []string // cataloged but the file is gone or has the wrong size: unreadable, never silently dropped
	Removed int      // files in the resource directory that no catalog row owns (dead writes, temporaries)
}

// Recover loads the catalog after a start and removes what a crashed run left
// in the resource directory: unfinished writes and sealing temporaries have no
// catalog row. It must run before any grant is issued and only once.
func (m *Manager) Recover(ctx context.Context) (Recovery, error) {
	var out Recovery
	m.mu.Lock()
	catalog, empty := m.catalog, len(m.res) == 0
	m.mu.Unlock()
	if catalog == nil || !empty {
		return out, ErrInvalid
	}
	entries, err := catalog.List(ctx)
	if err != nil {
		return out, ErrCatalog
	}
	owned := map[string]bool{}
	m.mu.Lock()
	for _, e := range entries {
		path := e.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.dir, path)
		}
		if filepath.Dir(path) == m.dir {
			owned[filepath.Base(path)] = true
		}
		rec := &record{id: e.ID, job: e.Job, path: path, version: e.Version, st: frozen, digest: e.Digest, size: e.Size}
		if info, serr := os.Stat(path); serr != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
			rec.st = tampered
			out.Missing = append(out.Missing, e.ID)
		}
		m.res[e.ID] = rec
		out.Loaded++
	}
	m.mu.Unlock()
	sort.Strings(out.Missing)
	files, err := os.ReadDir(m.dir)
	if err != nil {
		return out, ErrCatalog
	}
	for _, f := range files {
		if f.IsDir() || owned[f.Name()] {
			continue
		}
		if os.Remove(filepath.Join(m.dir, f.Name())) == nil {
			out.Removed++
		}
	}
	return out, nil
}

// Purge deletes every finished resource of a job (retention, cancellation
// cleanup): catalog rows first, files after, so a crash in between leaves
// files the next Recover removes rather than rows pointing at nothing. A job
// with a write in progress is refused; revoke its attempt first.
func (m *Manager) Purge(ctx context.Context, job string) (int, error) {
	m.mu.Lock()
	var victims []*record
	for _, r := range m.res {
		if r.job != job {
			continue
		}
		if r.st == open || r.st == sealing {
			m.mu.Unlock()
			return 0, ErrInvalid
		}
		if r.st == abandoned {
			delete(m.res, r.id) // nothing on disk or in the catalog; just forget it
			continue
		}
		victims = append(victims, r)
	}
	catalog := m.catalog
	for _, r := range victims {
		delete(m.res, r.id)
	}
	m.mu.Unlock()
	var first error
	for _, r := range victims {
		if catalog != nil {
			if err := catalog.Remove(ctx, r.id); err != nil {
				first = ErrCatalog
				continue // keep the file: its row still owns it
			}
		}
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = ErrNotWritable
		}
	}
	return len(victims), first
}

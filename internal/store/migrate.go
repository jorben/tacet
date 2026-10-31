package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var (
	ErrSchemaTooNew   = errors.New("SCHEMA_VERSION_UNSUPPORTED") // stored schema is newer than this build (AR-18)
	ErrMigrationFail  = errors.New("MIGRATION_FAILED")
	ErrMigrationSetup = errors.New("MIGRATION_DEFINITION_INVALID")
	ErrBackupFailed   = errors.New("MIGRATION_BACKUP_FAILED")
)

// Migration moves one module's tables from Version-1 to Version. Migrations
// only go forward; there is no down step (architecture 7.3).
type Migration struct {
	Version int
	SQL     []string
}

// Module is one module's registration: its table namespace and migrations. A
// module writes only tables carrying its prefix (MD-01).
type Module struct {
	Name       string
	Prefix     string // every table of the module starts with this
	Migrations []Migration
}

var (
	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9]{1,30}$`)
	prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,30}_$`)
)

// Registry is the set of modules for one database. Prefixes must not overlap,
// otherwise one module could legitimately name another one's tables.
type Registry struct{ mods []Module }

func NewRegistry(mods ...Module) (*Registry, error) {
	seenName, seen := map[string]bool{}, []string{}
	for _, m := range mods {
		if !namePattern.MatchString(m.Name) || !prefixPattern.MatchString(m.Prefix) || seenName[m.Name] {
			return nil, ErrMigrationSetup
		}
		seenName[m.Name] = true
		for _, p := range seen {
			if hasPrefix(p, m.Prefix) || hasPrefix(m.Prefix, p) {
				return nil, ErrMigrationSetup
			}
		}
		seen = append(seen, m.Prefix)
		last := 0
		for _, mig := range m.Migrations {
			if mig.Version != last+1 || len(mig.SQL) == 0 {
				return nil, ErrMigrationSetup // versions are dense: 1, 2, 3 ...
			}
			last = mig.Version
		}
	}
	sorted := append([]Module(nil), mods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return &Registry{mods: sorted}, nil
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func (m Module) latest() int {
	if len(m.Migrations) == 0 {
		return 0
	}
	return m.Migrations[len(m.Migrations)-1].Version
}

const coreSchema = `
CREATE TABLE IF NOT EXISTS store_schema (module TEXT PRIMARY KEY, version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS store_outbox (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL,
  kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL, digest TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS store_cursor (consumer TEXT PRIMARY KEY, last_seq INTEGER NOT NULL);`

// Backup copies the database before a migration touches it and returns a
// function that puts the copy back. The workspace holds the exclusive lock
// during migration, so no one writes meanwhile.
type Backup func(ctx context.Context) (restore func() error, discard func() error, err error)

// Migrate brings every module to its latest version in one transaction. A
// database already ahead of this build is refused before any change (AR-18).
// If anything fails, the transaction is rolled back and the backup, when one
// was taken, is restored; the caller must then refuse to use the workspace.
func (r *Registry) Migrate(ctx context.Context, db *sql.DB, backup Backup) (err error) {
	// Inspect before creating any tables or opening a migration transaction.
	// Even core-schema bootstrap must follow the backup and version refusal.
	var hasSchema int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='store_schema'`).Scan(&hasSchema); err != nil {
		return ErrMigrationFail
	}
	have := map[string]int{}
	if hasSchema > 0 {
		rows, queryErr := db.QueryContext(ctx, `SELECT module, version FROM store_schema`)
		if queryErr != nil {
			return ErrMigrationFail
		}
		for rows.Next() {
			var name string
			var version int
			if err = rows.Scan(&name, &version); err != nil || version < 0 {
				rows.Close()
				return ErrMigrationFail
			}
			have[name] = version
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return ErrMigrationFail
		}
	}
	known := map[string]Module{}
	for _, m := range r.mods {
		known[m.Name] = m
	}
	pending := hasSchema == 0
	for name, v := range have {
		m, ok := known[name]
		if !ok || v > m.latest() {
			return ErrSchemaTooNew // unknown module data or newer version: don't touch
		}
	}
	for _, m := range r.mods {
		if have[m.Name] < m.latest() {
			pending = true
		}
	}
	if !pending {
		return nil
	}
	var restore, discard func() error
	if backup != nil {
		if restore, discard, err = backup(ctx); err != nil {
			return ErrBackupFailed
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ErrMigrationFail
	}
	fail := func() error {
		tx.Rollback()
		if restore != nil {
			if rerr := restore(); rerr != nil {
				return fmt.Errorf("%w: restore failed", ErrMigrationFail)
			}
		}
		return ErrMigrationFail
	}
	if _, err = tx.ExecContext(ctx, coreSchema); err != nil {
		return fail()
	}
	for _, m := range r.mods {
		for _, mig := range m.Migrations {
			if mig.Version <= have[m.Name] {
				continue
			}
			for _, stmt := range mig.SQL {
				if err = checkOwned(m, stmt); err != nil {
					return fail()
				}
				if _, err = tx.ExecContext(ctx, stmt); err != nil {
					return fail()
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO store_schema(module, version) VALUES(?, ?)
				ON CONFLICT(module) DO UPDATE SET version = excluded.version`, m.Name, mig.Version); err != nil {
				return fail()
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return fail()
	}
	if discard != nil {
		_ = discard() // a leftover backup is removed at the next normal start
	}
	return nil
}

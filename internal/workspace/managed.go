package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrKeyMissing = errors.New("WORKSPACE_KEY_MISSING")
	ErrDBDamaged  = errors.New("WORKSPACE_DATABASE_UNUSABLE")
)

const (
	databaseName = "workspace.db"
	keysDirName  = "keys"
	// WorkspaceKeyID is the id keys.Manager derives batch and task keys from.
	workspaceKeyID = "workspace"
)

// Managed describes how a production workspace is opened.
type Managed struct {
	UserKey  keys.UserKeyStore // the per-OS-user key (Keychain / DPAPI)
	Registry *store.Registry   // module migrations
	Versions Versions          // job table admission facts
	// Create allows a brand new workspace (and, with it, a new key hierarchy).
	// Without it an absent database is an error, so a CLI run can never start
	// an empty workspace that silently hides existing data.
	Create bool
}

// Keys exposes the key manager of an open managed workspace (task and batch
// keys). It is nil for a workspace opened with Open.
func (w *Workspace) Keys() *keys.Manager { return w.keys }

// OpenManaged opens the production workspace in dir in this order, each step
// only after the previous one succeeded:
//
//  1. take ownership (OS lock, durable new generation), so nothing below runs
//     while another process could use the same files;
//  2. settle a backup left by a crash, then unwrap the workspace key;
//  3. open the encrypted database, migrate with a verified backup, and serve
//     commits through it.
//
// An existing database whose key is missing is an error (ErrKeyMissing): a key
// is created only together with a new database, because a fresh key could never
// read the old data. Any failure releases everything taken so far.
func OpenManaged(ctx context.Context, dir string, o Managed) (*Workspace, error) {
	if o.UserKey == nil || o.Registry == nil {
		return nil, ErrOpenFailed
	}
	w, real, err := acquire(dir)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			w.closed = true // not yet visible to anyone; Close would report ErrClosed
			for i := len(w.closers) - 1; i >= 0; i-- {
				w.closers[i]()
			}
			closeHeader(w)
		}
	}()
	dbPath := filepath.Join(real, databaseName)
	_, statErr := os.Stat(dbPath)
	exists := statErr == nil
	if !exists && !o.Create {
		return nil, ErrOpenFailed
	}
	wrapped, err := keys.NewDirWrapped(filepath.Join(real, keysDirName))
	if err != nil {
		return nil, ErrOpenFailed
	}
	mgr, err := keys.Open(o.UserKey, wrapped, o.Create && !exists)
	if err != nil {
		if errors.Is(err, keys.ErrKeyNotFound) {
			return nil, ErrKeyMissing
		}
		return nil, err
	}
	w.keys = mgr
	w.closers = append(w.closers, func() error { mgr.Close(); return nil })
	key, err := mgr.Get(keys.Workspace, workspaceKeyID)
	switch {
	case err == nil:
	case errors.Is(err, keys.ErrKeyNotFound) && !exists && o.Create:
		if key, err = mgr.Create(keys.Workspace, workspaceKeyID); err != nil {
			return nil, err
		}
	case errors.Is(err, keys.ErrKeyNotFound):
		return nil, ErrKeyMissing
	default:
		return nil, err
	}
	defer keys.Zero(key)
	if _, err = store.SweepBackup(ctx, dbPath, key); err != nil {
		return nil, ErrDBDamaged
	}
	raw, err := store.OpenEncrypted(ctx, dbPath, key)
	if err != nil {
		return nil, ErrDBDamaged
	}
	db, err := store.Open(ctx, raw, o.Registry, store.FileBackup(raw, dbPath, key))
	if err != nil {
		raw.Close()
		return nil, err
	}
	w.closers = append(w.closers, db.Close)
	w.backend = BackendFunc(func(ctx context.Context) (Txn, error) { return db.Begin(ctx) })
	w.versions = o.Versions
	ok = true
	return w, nil
}

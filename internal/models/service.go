package models

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/netgate"
	"github.com/jorben/tacet/internal/store"
)

// State is the resource state of one model (module design 9.5).
type State string

const (
	StatePending     State = "pending"     // 待下载
	StateDownloading State = "downloading" // 下载中
	StatePaused      State = "paused"      // 已暂停
	StateVerifying   State = "verifying"   // 校验中
	StatePreparing   State = "preparing"   // 准备中
	StateReady       State = "ready"       // 已就绪 (this session)
	StateError       State = "error"       // 可恢复错误
	// StateInstalled: files are registered and verified but no worker has loaded the
	// model in this session. The interface shows it as "准备中" with a prepare action.
	StateInstalled State = "installed"
)

// Codes of the recoverable error state.
const (
	CodeNetwork  = "MODEL_NETWORK"
	CodeDisk     = "MODEL_DISK_SPACE"
	CodeVerify   = "MODEL_VERIFY_FAILED"
	CodePrepare  = "MODEL_PREPARE_FAILED"
	CodeChanged  = "MODEL_FILES_CHANGED"
	CodeInternal = "MODEL_STORE_FAILED"
)

var (
	ErrPaused          = errors.New("MODEL_DOWNLOAD_PAUSED")
	ErrBusy            = errors.New("MODEL_OPERATION_IN_PROGRESS")
	ErrNotDownloadable = errors.New("MODEL_NOT_DOWNLOADABLE")
	ErrNotInstalled    = errors.New("MODEL_NOT_INSTALLED")
	ErrBindingMissing  = errors.New("MODEL_BINDING_MISSING")
	ErrBindingMismatch = errors.New("MODEL_BINDING_MISMATCH")
	ErrFailed          = errors.New("MODEL_OPERATION_FAILED")
	ErrConfig          = errors.New("MODEL_CONFIG_INVALID")
)

func Module() store.Module {
	return store.Module{Name: "models", Prefix: "models_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE models_state(key TEXT PRIMARY KEY, state TEXT NOT NULL, code TEXT NOT NULL)`,
		`CREATE TABLE models_part(key TEXT PRIMARY KEY, file TEXT NOT NULL, entity TEXT NOT NULL)`,
	}}}}
}

// Config wires the service. Everything that touches the outside world is injected:
// the network goes through netgate, the disk-space query and the model session
// (load plus fixed sample run, done by a worker) belong to modules models may not
// import.
type Config struct {
	Commit  func(ctx context.Context, fn func(store.Tx) error) error
	Root    string // absolute; holds staging/ and installed/
	Gate    *netgate.Gate
	Hosts   []string
	TLS     *tls.Config
	URL     func(e Entry, file string) (string, error)
	Free    func(dir string) (int64, error)
	Prepare func(ctx context.Context, dir string, e Entry) error
	Catalog []Entry
}

type job struct {
	cancel context.CancelFunc
	paused bool
}

type Service struct {
	cfg      Config
	entries  map[string]Entry
	mu       sync.Mutex
	active   map[string]*job
	ready    map[string]bool
	verified map[string]string // key -> stamp of the files last hashed in full; CheckBinding skips the hash when it matches
}

func New(cfg Config) (*Service, error) {
	if cfg.Commit == nil || cfg.Gate == nil || cfg.URL == nil || cfg.Free == nil || cfg.Prepare == nil || !filepath.IsAbs(cfg.Root) || len(cfg.Catalog) == 0 {
		return nil, ErrConfig
	}
	s := &Service{cfg: cfg, entries: map[string]Entry{}, active: map[string]*job{}, ready: map[string]bool{}, verified: map[string]string{}}
	for _, e := range cfg.Catalog {
		if e.Digest == "" {
			return nil, ErrConfig
		}
		if _, dup := s.entries[e.Key()]; dup {
			return nil, ErrConfig
		}
		s.entries[e.Key()] = e
	}
	for _, d := range []string{"staging", "installed"} {
		if err := os.MkdirAll(filepath.Join(cfg.Root, d), 0o700); err != nil {
			return nil, ErrConfig
		}
	}
	return s, nil
}

func (s *Service) staging(e Entry) string   { return filepath.Join(s.cfg.Root, "staging", e.Key()) }
func (s *Service) installed(e Entry) string { return filepath.Join(s.cfg.Root, "installed", e.Key()) }

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	sc, err := t.Module("models")
	return t, sc, err
}

func (s *Service) set(ctx context.Context, e Entry, st State, code string) error {
	return s.cfg.Commit(context.WithoutCancel(ctx), func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		if _, err = sc.Exec(`INSERT INTO models_state(key,state,code) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET state=excluded.state, code=excluded.code`, e.Key(), string(st), code); err != nil {
			return err
		}
		tx.Emit(store.Event{Type: "ModelChanged", Subject: kernel.ArtifactRef{Kind: "model", ID: e.Key(), Digest: e.Digest}})
		return nil
	})
}

func (s *Service) stored(ctx context.Context, e Entry) (State, string, error) {
	st, code := StatePending, ""
	err := s.cfg.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		var v string
		switch err = t.QueryRow("SELECT state, code FROM models_state WHERE key=?", e.Key()).Scan(&v, &code); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		st = State(v)
		return nil
	})
	return st, code, err
}

func (s *Service) entity(ctx context.Context, e Entry) (file, entity string) {
	s.cfg.Commit(ctx, func(tx store.Tx) error {
		t, _, err := scope(tx)
		if err != nil {
			return err
		}
		t.QueryRow("SELECT file, entity FROM models_part WHERE key=?", e.Key()).Scan(&file, &entity)
		return nil
	})
	return
}

func (s *Service) setEntity(ctx context.Context, e Entry, file, entity string) error {
	return s.cfg.Commit(ctx, func(tx store.Tx) error {
		_, sc, err := scope(tx)
		if err != nil {
			return err
		}
		if entity == "" {
			_, err = sc.Exec("DELETE FROM models_part WHERE key=?", e.Key())
			return err
		}
		_, err = sc.Exec(`INSERT INTO models_part(key,file,entity) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET file=excluded.file, entity=excluded.entity`, e.Key(), file, entity)
		return err
	})
}

// Status is what the interface shows for one model.
type Status struct {
	Entry      Entry
	State      State
	Code       string
	Downloaded int64 // bytes held in staging (or all of them once installed)
	Total      int64
}

func dirSize(dir string) (n int64) {
	es, _ := os.ReadDir(dir)
	for _, f := range es {
		if info, err := f.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
	}
	return n
}

// List returns every model of the catalog in key order. A model nothing was ever
// done to is "pending"; the service keeps no "current model" (model choice belongs
// to run presets).
func (s *Service) List(ctx context.Context) ([]Status, error) {
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Status
	for _, k := range keys {
		e := s.entries[k]
		st, code, err := s.stored(ctx, e)
		if err != nil {
			return nil, ErrFailed
		}
		s.mu.Lock()
		if st == StateReady && !s.ready[k] {
			st = StateInstalled
		}
		s.mu.Unlock()
		done := dirSize(s.staging(e))
		if st == StateInstalled || st == StateReady || st == StatePreparing {
			done = e.Total()
		}
		out = append(out, Status{Entry: e, State: st, Code: code, Downloaded: done, Total: e.Total()})
	}
	return out, nil
}

func (s *Service) entry(key string) (Entry, error) {
	e, ok := s.entries[key]
	if !ok {
		return Entry{}, ErrUnknown
	}
	return e, nil
}

// Recover runs once at start-up. It never touches the network: a download that was
// running when the program ended becomes "paused", and the interface offers to
// continue it (architecture 8.3). A model that was ready is installed again until a
// worker has loaded it in this session.
func (s *Service) Recover(ctx context.Context) error {
	for _, e := range s.entries {
		st, code, err := s.stored(ctx, e)
		if err != nil {
			return ErrFailed
		}
		next := st
		if _, err := os.Stat(s.installed(e)); err == nil && st == StatePending {
			// another workspace of the same user installed it into the shared folder; its files are
			// checked before any use (CheckBinding, the plugin's own verification)
			next = StateInstalled
		}
		switch st {
		case StateDownloading, StateVerifying:
			next = StatePaused
		case StatePreparing, StateReady:
			next = StateInstalled
		}
		if _, err := os.Stat(s.installed(e)); err != nil && (next == StateInstalled) {
			next, code = StateError, CodeChanged
		}
		if next != st {
			if err := s.set(ctx, e, next, code); err != nil {
				return ErrFailed
			}
		}
	}
	return nil
}

// Dir is where a model's files are once installed (whether or not they are there now).
func (s *Service) Dir(key string) string {
	e, ok := s.entries[key]
	if !ok {
		return ""
	}
	return s.installed(e)
}

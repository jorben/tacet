package envs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

var (
	ErrInvalid     = errors.New("ENV_REQUEST_INVALID")
	ErrNotFound    = errors.New("ENV_NOT_FOUND")
	ErrExists      = errors.New("ENV_ALREADY_REGISTERED")
	ErrNotReady    = errors.New("ENV_NOT_READY")
	ErrBuiltin     = errors.New("ENV_BUILTIN_READ_ONLY")
	ErrDigest      = errors.New("ENV_DIGEST_MISMATCH")
	ErrInterpreter = errors.New("ENV_INTERPRETER_INVALID")
	ErrOutsideBase = errors.New("ENV_PATH_OUTSIDE_BASE")
	ErrProbeFailed = errors.New("ENV_PROBE_FAILED")
)

// State of a registered environment (module design 9.4). Only Ready can be
// scheduled: a half-deployed environment is never launchable.
type State string

const (
	Deploying     State = "deploying"
	Ready         State = "ready"
	Broken        State = "broken"
	NeedsRedeploy State = "needs_redeploy"
)

// Identity is what makes two environments the same: platform, interpreter ABI,
// the lock file and the installed artifacts. The digest is derived from it, so
// a record cannot claim a digest it does not have.
type Identity struct {
	OSArch          string // "darwin-arm64", "windows-amd64"
	PythonABI       string // e.g. "cp313"
	LockDigest      string // sha256 of uv.lock
	ArtifactsDigest string // sha256 over installed files, native libraries included
}

var hexPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var abiPattern = regexp.MustCompile(`^[a-z]{2,}[0-9]{2,4}$`)
var osArchPattern = regexp.MustCompile(`^[a-z]+-[a-z0-9_]+$`)

func (i Identity) valid() bool {
	return osArchPattern.MatchString(i.OSArch) && abiPattern.MatchString(i.PythonABI) &&
		hexPattern.MatchString(i.LockDigest) && hexPattern.MatchString(i.ArtifactsDigest)
}

// DigestOf is the environment's identifier.
func DigestOf(i Identity) (string, error) {
	if !i.valid() {
		return "", ErrInvalid
	}
	h := sha256.New()
	for _, part := range []string{"tacet/env/v1", i.OSArch, i.PythonABI, i.LockDigest, i.ArtifactsDigest} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Env is a registered environment.
type Env struct {
	Digest      string
	Identity    Identity
	Root        string // absolute, inside the repository base
	Interpreter string // relative to Root
	Builtin     bool
	State       State
}

// Module owns the environment table in the application database.
func Module() store.Module {
	return store.Module{Name: "envs", Prefix: "envs_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE envs_env(digest TEXT PRIMARY KEY, root TEXT NOT NULL, interpreter TEXT NOT NULL,
			os_arch TEXT NOT NULL, abi TEXT NOT NULL, lock_digest TEXT NOT NULL, artifacts_digest TEXT NOT NULL,
			builtin INTEGER NOT NULL, state TEXT NOT NULL)`,
	}}}}
}

// Repository registers environments below base. Builtin environments live in
// the signed application bundle and may be outside base; they are read-only.
type Repository struct{ base string }

func NewRepository(base string) (*Repository, error) {
	real, err := filepath.EvalSymlinks(base)
	if err != nil || !filepath.IsAbs(real) {
		return nil, ErrInvalid
	}
	return &Repository{base: real}, nil
}

func scope(tx store.Tx) (*store.Txn, *store.Scope, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	s, err := t.Module("envs")
	return t, s, err
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// plainRelative reports whether rel is a non-empty path below a root on every
// platform: not absolute, not rooted ("/x" and "\x" are relative to the drive
// on Windows), no volume name ("C:x") and no "..".
func plainRelative(rel string) bool {
	return rel != "" && !filepath.IsAbs(rel) && filepath.VolumeName(rel) == "" &&
		!strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "\\") && !strings.Contains(rel, "..")
}

// resolveInterpreter checks that the recorded interpreter is a relative path
// without "..", and that what it names is a regular, executable file. A venv
// interpreter is normally a symlink to the managed CPython outside the venv, so
// symlink targets are NOT required to stay inside the root; the digest of the
// installed artifacts (not this check) is what vouches for the runtime. The
// returned path keeps the venv location, which the interpreter needs to find
// its pyvenv.cfg and site-packages.
func resolveInterpreter(root, rel string) (string, error) {
	if !plainRelative(rel) {
		return "", ErrInterpreter
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrInterpreter
	}
	launch := filepath.Join(realRoot, rel)
	info, err := os.Stat(launch) // follows the symlink: the target must exist
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrInterpreter
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", ErrInterpreter
	}
	return launch, nil
}

// BeginDeploy records an environment as Deploying. It cannot be launched until
// Finish succeeds; a crashed deployment leaves a Deploying row that Reclaim
// removes.
func (r *Repository) BeginDeploy(tx store.Tx, id Identity, root, interpreter string, builtin bool) (string, error) {
	digest, err := DigestOf(id)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) || (!builtin && !within(r.base, filepath.Clean(root))) {
		return "", ErrOutsideBase
	}
	if !plainRelative(interpreter) {
		return "", ErrInterpreter
	}
	t, sc, err := scope(tx)
	if err != nil {
		return "", err
	}
	var n int
	if err = t.QueryRow(`SELECT COUNT(*) FROM envs_env WHERE digest=?`, digest).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "", ErrExists
	}
	_, err = sc.Exec(`INSERT INTO envs_env(digest, root, interpreter, os_arch, abi, lock_digest, artifacts_digest, builtin, state)
		VALUES(?,?,?,?,?,?,?,?,?)`, digest, filepath.Clean(root), interpreter, id.OSArch, id.PythonABI, id.LockDigest, id.ArtifactsDigest, builtin, string(Deploying))
	return digest, err
}

// Probe starts the environment's interpreter and confirms it can import what
// the plugin needs. It is supplied by the caller (host), because envs may not
// start processes.
type Probe func(Launch) error

// Finish settles a Deploying environment: Ready after the interpreter file
// check and the caller's probe both pass, otherwise Broken. A failed check is a
// recorded outcome, not an error, so the caller commits the Broken mark; error
// is only for problems that prevented deciding (unknown digest, wrong state,
// database failure). Ready never precedes a passed check, and Broken cannot be
// revived: it must be redeployed.
func (r *Repository) Finish(tx store.Tx, digest string, probe Probe) (State, error) {
	env, err := r.Get(tx, digest)
	if err != nil {
		return "", err
	}
	if env.State != Deploying || probe == nil {
		return "", ErrInvalid
	}
	_, sc, err := scope(tx)
	if err != nil {
		return "", err
	}
	next := Ready
	if _, err = resolveInterpreter(env.Root, env.Interpreter); err != nil {
		next = Broken
	} else if probe(r.launch(env, "tacet_probe", nil, "")) != nil {
		next = Broken
	}
	if _, err = sc.Exec(`UPDATE envs_env SET state=? WHERE digest=?`, string(next), digest); err != nil {
		return "", err
	}
	return next, nil
}

func (r *Repository) Get(tx store.Tx, digest string) (Env, error) {
	t, _, err := scope(tx)
	if err != nil {
		return Env{}, err
	}
	var e Env
	var state string
	err = t.QueryRow(`SELECT digest, root, interpreter, os_arch, abi, lock_digest, artifacts_digest, builtin, state FROM envs_env WHERE digest=?`, digest).
		Scan(&e.Digest, &e.Root, &e.Interpreter, &e.Identity.OSArch, &e.Identity.PythonABI, &e.Identity.LockDigest, &e.Identity.ArtifactsDigest, &e.Builtin, &state)
	if err != nil {
		return Env{}, ErrNotFound
	}
	e.State = State(state)
	return e, nil
}

// Launch is everything host needs to start a worker; it carries no shell
// string and no inherited environment.
type Launch struct {
	Path string   // absolute interpreter
	Args []string // after Path
	Dir  string   // working directory, absolute
	Env  []string // complete environment
}

// whitelist is the only environment a plugin receives. PYTHON* variables of
// the parent are never forwarded, and bytecode is not written because builtin
// environments are read-only.
func whitelist() []string {
	env := []string{"PYTHONDONTWRITEBYTECODE=1", "PYTHONUTF8=1", "PYTHONNOUSERSITE=1"}
	if runtime.GOOS == "windows" {
		return append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return append(env, "PATH=/usr/bin:/bin")
}

func (r *Repository) launch(e Env, module string, args []string, dir string) Launch {
	// -I: isolated mode ignores PYTHON* variables, the user site and puts neither
	// the working directory nor the script directory on sys.path, so a planted
	// json.py next to the document cannot be imported.
	full := append([]string{"-I", "-m", module}, args...)
	return Launch{Path: filepath.Join(e.Root, e.Interpreter), Args: full, Dir: dir, Env: whitelist()}
}

// LaunchSpec returns the launch description for a Ready environment. The
// module must be a dotted Python module name, the working directory an existing
// absolute directory (a private scratch area, not the document's directory).
func (r *Repository) LaunchSpec(tx store.Tx, digest, module string, args []string, dir string) (Launch, error) {
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`).MatchString(module) {
		return Launch{}, ErrInvalid
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || !filepath.IsAbs(dir) {
		return Launch{}, ErrInvalid
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return Launch{}, ErrInvalid
		}
	}
	e, err := r.Get(tx, digest)
	if err != nil {
		return Launch{}, err
	}
	if e.State != Ready {
		return Launch{}, ErrNotReady
	}
	if _, err = resolveInterpreter(e.Root, e.Interpreter); err != nil {
		return Launch{}, err // the file changed after registration: refuse, do not guess
	}
	return r.launch(e, module, args, dir), nil
}

// MarkBroken is used when a health check or launch finds the environment damaged.
func (r *Repository) MarkBroken(tx store.Tx, digest string) error {
	e, err := r.Get(tx, digest)
	if err != nil {
		return err
	}
	if e.Builtin {
		return ErrBuiltin
	}
	_, sc, err := scope(tx)
	if err != nil {
		return err
	}
	_, err = sc.Exec(`UPDATE envs_env SET state=? WHERE digest=?`, string(Broken), digest)
	return err
}

// MarkRedeploy is called after an application update. Third-party environments
// built for another Python ABI or platform become NeedsRedeploy; builtin ones
// ship with the app and are never touched. It returns the affected digests,
// and emits PluginChanged-style events through the caller if needed.
func (r *Repository) MarkRedeploy(tx store.Tx, osArch, abi string) ([]string, error) {
	t, sc, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT digest FROM envs_env WHERE builtin=0 AND state IN ('ready','broken') AND (abi<>? OR os_arch<>?) ORDER BY digest`, abi, osArch)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var d string
		if err = rows.Scan(&d); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, d := range out {
		if _, err = sc.Exec(`UPDATE envs_env SET state=? WHERE digest=?`, string(NeedsRedeploy), d); err != nil {
			return nil, err
		}
	}
	tx.Emit(store.Event{Type: "EnvironmentsChanged", Subject: kernel.ArtifactRef{Kind: "environment", ID: "all"}})
	return out, nil
}

// Referenced answers whether an environment is still used by an installed
// plugin, a running job or a recoverable snapshot (registry owns the references).
type Referenced func(tx store.Tx, digest string) (bool, error)

// Reclaim removes the rows of third-party environments nobody references and
// returns their roots; the caller deletes the directories after the commit
// (RemoveRoot). Builtin environments and referenced ones are kept, whatever
// state they are in.
func (r *Repository) Reclaim(tx store.Tx, referenced Referenced) ([]string, error) {
	if referenced == nil {
		return nil, ErrInvalid
	}
	t, sc, err := scope(tx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(`SELECT digest, root FROM envs_env WHERE builtin=0 ORDER BY digest`)
	if err != nil {
		return nil, err
	}
	type victim struct{ digest, root string }
	var all []victim
	for rows.Next() {
		var v victim
		if err = rows.Scan(&v.digest, &v.root); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var roots []string
	for _, v := range all {
		used, err := referenced(tx, v.digest)
		if err != nil {
			return nil, err
		}
		if used {
			continue
		}
		if _, err = sc.Exec(`DELETE FROM envs_env WHERE digest=?`, v.digest); err != nil {
			return nil, err
		}
		roots = append(roots, v.root)
	}
	sort.Strings(roots)
	return roots, nil
}

// RemoveRoot deletes a reclaimed environment directory, but only if it lies
// strictly inside the repository base.
func (r *Repository) RemoveRoot(root string) error {
	real, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || real == r.base || !within(r.base, real) {
		return ErrOutsideBase
	}
	return os.RemoveAll(real)
}

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jorben/tacet/internal/keys"
	"github.com/jorben/tacet/internal/platform"
)

// Config says where one process keeps its workspace and finds the release
// group's built-in components. The desktop app and the CLI differ only in the
// workspace they pass (a CLI default is independent of the GUI's).
type Config struct {
	Workspace string            // absolute; created on first use
	Root      string            // release root; empty: TACET_ROOT or next to the executable
	UserKey   keys.UserKeyStore // empty: the platform credential store
	Account   string            // credential account of the workspace; default "workspace"
}

// ErrRuntimeMissing says the built-in components are not where they should be.
var ErrRuntimeMissing = errors.New("RUNTIME_MISSING")

// DefaultRoot finds the release root: TACET_ROOT, else the nearest ancestor of
// the executable that holds resources/rules.
func DefaultRoot() (string, error) {
	if r := os.Getenv("TACET_ROOT"); r != "" {
		return r, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", ErrRuntimeMissing
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	for d := filepath.Dir(exe); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, "resources", "rules")); err == nil && fi.IsDir() {
			return d, nil
		}
		if d == filepath.Dir(d) {
			return "", ErrRuntimeMissing
		}
	}
}

// DefaultWorkspace is the per-user workspace of an entry point ("cli", "desktop").
func DefaultWorkspace(entry string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Tacet", entry+"-workspace"), nil
}

func platformID() (string, error) {
	switch p := runtime.GOOS + "-" + runtime.GOARCH; p {
	case "darwin-arm64":
		return p, nil
	case "windows-amd64":
		return "windows-amd64", nil
	}
	return "", fmt.Errorf("%w: unsupported platform", ErrRuntimeMissing)
}

// Locate reads the built-in plugins, rule pack and interpreter below root.
func Locate(root string) (Options, error) {
	var o Options
	var err error
	if o.Platform, err = platformID(); err != nil {
		return o, err
	}
	o.Python = filepath.Join(root, "python", ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		o.Python = filepath.Join(root, "python", ".venv", "Scripts", "python.exe")
	}
	if _, err = os.Stat(o.Python); err != nil {
		return o, fmt.Errorf("%w: python environment", ErrRuntimeMissing)
	}
	if o.RulePack, err = os.ReadFile(filepath.Join(root, "resources", "rules", "cn-office-v1.json")); err != nil {
		return o, fmt.Errorf("%w: rule pack", ErrRuntimeMissing)
	}
	h := sha256.New()
	h.Write([]byte(o.Python))
	if lock, err := os.ReadFile(filepath.Join(root, "python", "uv.lock")); err == nil {
		h.Write(lock)
	}
	o.EnvDigest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	locateModels(root, os.Getenv("TACET_PREINSTALLED_MODELS"), &o)
	manifests, _ := filepath.Glob(filepath.Join(root, "python", "plugins", "*", "manifest.json"))
	sort.Strings(manifests)
	for _, m := range manifests {
		data, err := os.ReadFile(m)
		if err != nil {
			return o, ErrRuntimeMissing
		}
		var man struct {
			Roles   []string `json:"roles"`
			Formats []string `json:"formats"`
			Runtime struct {
				Entry string `json:"entry"`
			} `json:"runtime"`
		}
		if json.Unmarshal(data, &man) != nil {
			return o, ErrRuntimeMissing
		}
		if len(man.Formats) == 0 || !has(man.Roles, "format") {
			continue
		}
		o.Plugins = append(o.Plugins, Builtin{Module: man.Runtime.Entry, Manifest: data, Formats: man.Formats})
	}
	if len(o.Plugins) == 0 {
		return o, fmt.Errorf("%w: plugins", ErrRuntimeMissing)
	}
	return o, nil
}

// catalogModels are the models of the catalog in the order the interface lists them (the first is the
// default model), with GPU-compatible weights shared by GPU and CPU execution.
var catalogModels = []struct {
	manifest, plugin, module string
}{
	{"astrlink-guard-fp16.json", "detect-astrlink", "tacet_detect_astrlink"},
	{"privacy-filter-fp16.json", "detect-privacy-filter", "tacet_detect_privacy_filter"},
}

// locateModels reads the model catalog below root. The files are downloaded by the user; preinstalled, when
// set, is a folder whose "<id>-<variant>" subfolders hold files already present (development and tests). The
// same build is offered on every platform; the worker detects GPU support.
func locateModels(root, preinstalled string, o *Options) {
	dirOf := func(manifest string) string {
		if preinstalled == "" || !filepath.IsAbs(preinstalled) {
			return ""
		}
		d := filepath.Join(preinstalled, strings.TrimSuffix(manifest, ".json"))
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		return ""
	}
	for _, c := range catalogModels {
		manifest := filepath.Join(root, "resources", "models", c.manifest)
		plug, err := os.ReadFile(filepath.Join(root, "python", "plugins", c.plugin, "manifest.json"))
		if err != nil {
			continue
		}
		if _, err := os.Stat(manifest); err != nil {
			continue
		}
		m := &ModelOption{Dir: dirOf(c.manifest), ManifestPath: manifest, Plugin: Builtin{Module: c.module, Manifest: plug}}
		o.Models = append(o.Models, m)
	}
}

// DefaultModelsRoot is the per-user folder downloaded models are kept in, shared by the desktop app and the
// command line so a model is downloaded once.
func DefaultModelsRoot() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Tacet", "models"), nil
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// OpenWith opens the engine described by c.
func OpenWith(ctx context.Context, c Config) (*Engine, error) {
	if !filepath.IsAbs(c.Workspace) {
		return nil, ErrOptions
	}
	root := c.Root
	var err error
	if root == "" {
		if root, err = DefaultRoot(); err != nil {
			return nil, err
		}
	}
	o, err := Locate(root)
	if err != nil {
		return nil, err
	}
	o.Dir = c.Workspace
	if o.ModelsRoot, err = DefaultModelsRoot(); err != nil {
		return nil, err
	}
	o.UserKey = c.UserKey
	if o.UserKey == nil {
		acct := c.Account
		if acct == "" {
			acct = "workspace"
		}
		if o.UserKey, err = keys.NewOSUserKeyStore(credentialService, credentialAccount(acct, c.Workspace)); err != nil {
			return nil, err
		}
	}
	_, statErr := os.Stat(filepath.Join(c.Workspace, "workspace.db"))
	o.Create = statErr != nil
	return Open(ctx, o)
}

// Interrupts are the signals that stop a command-line run (platform dependent).
func Interrupts() []os.Signal { return platform.TerminationSignals() }

const credentialService = "com.sophinova.tacet.workspace"

// credentialAccount names a workspace's key in the credential store: the kind and a digest of the workspace
// path. The store accepts only plain names (no ':' or path separators, Windows uses them in a file name),
// and the path itself, which may name the person, stays out of it.
func credentialAccount(kind, workspace string) string {
	return kind + "-" + hexSum([]byte(workspace))[:32]
}

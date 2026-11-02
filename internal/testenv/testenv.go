// Package testenv gives a package's tests a home of their own and finds the
// pieces of the repository they run against. Only _test.go files import it
// (internal/archtest holds every other package to the module design, where it
// has no place).
//
// Each package with tests runs them through it:
//
//	func TestMain(m *testing.M) { testenv.Main(m) }
//
// so no test reads or writes the files of the person running it: HOME,
// USERPROFILE, the XDG folders and Windows' APPDATA and LOCALAPPDATA all point
// into one new temporary folder, and os.UserHomeDir, os.UserConfigDir and
// os.UserCacheDir are checked to land there before any test runs. Setting HOME
// alone isn't that: Go reads the home from USERPROFILE on Windows.
// internal/archtest's TestEveryTestedPackageRunsInATestHome fails when a
// package with tests has no such TestMain.
package testenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	// Marker holds the temporary home in the environment. A test binary that a
	// test starts again (a helper process) inherits it and keeps the home its
	// parent made instead of making, and later removing, one of its own.
	Marker = "TACET_TEST_HOME"
	// RequirePlugins, set to 1, turns a missing plugin environment into a
	// failure instead of a skip. CI sets it, so the cross-language tests can't
	// pass there by not running.
	RequirePlugins = "TACET_REQUIRE_PLUGINS"
	// RealKeychain, set to 1, runs the tests that use the real login Keychain
	// (macOS). CI sets it; on a person's own machine they are opt-in, since they
	// add and remove items in that person's Keychain.
	RealKeychain = "TACET_REAL_KEYCHAIN"

	prefix = "tacet-test-home-"
)

// folderVars are the variables Isolate points into the temporary home.
var folderVars = []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"}

// realHome is the home the process started with, before Isolate.
var realHome = os.Getenv("HOME")

// Main runs the package's tests in a home of their own and exits with their code.
func Main(m *testing.M) { os.Exit(Run(m)) }

// Run isolates, runs the tests, removes the temporary home and gives the code
// to exit with. It runs no test when the home can't be made safe.
func Run(m *testing.M) int {
	if home := os.Getenv(Marker); home != "" && os.Getenv("HOME") == home && within(home, os.TempDir()) {
		return m.Run() // started again by one of the tests: keep its home
	}
	home, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer remove(home)
	return m.Run()
}

// Isolate points the home and the config, cache and data folders into a new
// temporary folder and gives the folder. Go's own caches stay where they were,
// so a test that runs the go command doesn't download every module again. It
// fails when Go would still resolve a user folder outside the new one.
func Isolate() (string, error) {
	pinGo()
	home, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	if home, err = filepath.EvalSymlinks(home); err != nil { // macOS: /var is /private/var
		return "", err
	}
	for k, v := range map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
		Marker:            home,
	} {
		if err := os.Setenv(k, v); err != nil {
			return "", err
		}
	}
	for name, dir := range map[string]func() (string, error){
		"home": os.UserHomeDir, "config": os.UserConfigDir, "cache": os.UserCacheDir,
	} {
		p, err := dir()
		if err != nil || !within(p, home) {
			return "", fmt.Errorf("the %s folder %q is outside the test home %s (%v)", name, p, home, err)
		}
	}
	return home, nil
}

// pinGo keeps GOPATH, GOMODCACHE, GOCACHE and GOENV at what they resolve to
// now, before the home moves: their defaults are derived from it.
func pinGo() {
	set := func(k, v string) {
		if os.Getenv(k) == "" && v != "" {
			os.Setenv(k, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		set("GOPATH", filepath.Join(home, "go"))
	}
	if gp := strings.Split(os.Getenv("GOPATH"), string(os.PathListSeparator))[0]; gp != "" {
		set("GOMODCACHE", filepath.Join(gp, "pkg", "mod"))
	}
	if cache, err := os.UserCacheDir(); err == nil {
		set("GOCACHE", filepath.Join(cache, "go-build"))
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		set("GOENV", filepath.Join(cfg, "go", "env"))
	}
}

// Root is the repository's root folder: the nearest ancestor of the working
// directory (a package's folder while its tests run) that holds go.mod.
func Root(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			t.Fatalf("no go.mod above %s", dir)
		}
	}
}

// VenvPython is the interpreter's path inside a virtual environment, with
// forward slashes: bin/python, or Scripts/python.exe on Windows.
var VenvPython = venvPython(runtime.GOOS)

func venvPython(goos string) string {
	if goos == "windows" {
		return "Scripts/python.exe"
	}
	return "bin/python"
}

// Python is the built-in plugins' interpreter, python/.venv's, which
// "uv sync --locked --all-packages" in python/ makes. Without it the test is
// skipped, or fails when RequirePlugins is 1.
func Python(t testing.TB) string {
	t.Helper()
	p := filepath.Join(Root(t), "python", ".venv", filepath.FromSlash(VenvPython))
	if _, err := os.Stat(p); err != nil {
		Missing(t, "the plugin environment %s (uv sync --locked --all-packages in python/)", p)
	}
	return p
}

// Missing skips the test for something the plugin environment should have
// given it, such as a fixture its interpreter builds, or fails it when
// RequirePlugins is 1: where the environment is installed, a missing piece is
// a fault, not a reason to pass.
func Missing(t testing.TB, format string, args ...any) {
	t.Helper()
	what := fmt.Sprintf(format, args...)
	if required() {
		t.Fatalf("needs %s, and %s=1 requires it", what, RequirePlugins)
	}
	t.Skipf("needs %s; set %s=1 to fail instead", what, RequirePlugins)
}

func required() bool { return os.Getenv(RequirePlugins) == "1" }

// UseRealKeychain lets a test reach the real login Keychain: it skips the test
// unless RealKeychain is 1, and gives it the home it started with, whose
// Keychain search list the Security framework reads.
func UseRealKeychain(t testing.TB) {
	t.Helper()
	if os.Getenv(RealKeychain) != "1" {
		t.Skipf("uses the real login Keychain; set %s=1 to run it", RealKeychain)
	}
	if realHome == "" {
		t.Fatal(errors.New("the process started with no HOME"))
	}
	t.Setenv("HOME", realHome)
}

// within says whether path lies in dir.
func within(path, dir string) bool {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// remove deletes the temporary home Isolate made, and nothing else.
func remove(home string) {
	if within(home, os.TempDir()) && strings.HasPrefix(filepath.Base(home), prefix) {
		os.RemoveAll(home)
	}
}

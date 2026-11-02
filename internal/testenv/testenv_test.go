package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// Every folder Go resolves for the user is inside the temporary home, and the
// home is not the one the process started with.
func TestUserFoldersAreInTheTestHome(t *testing.T) {
	home := os.Getenv(Marker)
	if home == "" || !within(home, os.TempDir()) {
		t.Fatalf("%s = %q, not a temporary folder", Marker, home)
	}
	if realHome != "" && within(realHome, home) {
		t.Fatalf("the test home %s is the real one %s", home, realHome)
	}
	for name, dir := range map[string]func() (string, error){
		"home": os.UserHomeDir, "config": os.UserConfigDir, "cache": os.UserCacheDir,
	} {
		p, err := dir()
		if err != nil || !within(p, home) {
			t.Errorf("%s folder %q is outside %s (%v)", name, p, home, err)
		}
	}
	for _, k := range folderVars {
		if v := os.Getenv(k); !within(v, home) {
			t.Errorf("%s = %q, outside %s", k, v, home)
		}
	}
}

// Go's caches keep the paths they had before the home moved.
func TestGoCachesStayPinned(t *testing.T) {
	home := os.Getenv(Marker)
	for _, k := range []string{"GOMODCACHE", "GOCACHE"} {
		v := os.Getenv(k)
		if v == "" || within(v, home) {
			t.Errorf("%s = %q, want a path outside the test home %s", k, v, home)
		}
	}
}

func TestVenvPythonPerOS(t *testing.T) {
	for goos, want := range map[string]string{"windows": "Scripts/python.exe", "darwin": "bin/python", "linux": "bin/python"} {
		if got := venvPython(goos); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}

func TestRootHoldsGoMod(t *testing.T) {
	if _, err := os.Stat(filepath.Join(Root(t), "go.mod")); err != nil {
		t.Fatal(err)
	}
}

// Only RequirePlugins=1 turns a missing piece into a failure; otherwise it skips.
func TestMissingSkipsUnlessRequired(t *testing.T) {
	for env, want := range map[string]bool{"": false, "0": false, "true": false, "1": true} {
		t.Setenv(RequirePlugins, env)
		if got := required(); got != want {
			t.Errorf("%s=%q: required %v, want %v", RequirePlugins, env, got, want)
		}
	}
	t.Setenv(RequirePlugins, "")
	var skipped bool
	t.Run("not required", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		Missing(t, "a fixture")
	})
	if !skipped {
		t.Error("a missing piece did not skip the test")
	}
}

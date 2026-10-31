//go:build darwin || windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockHelper(t *testing.T) {
	path := os.Getenv("TACET_TEST_LOCK_PATH")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.Exit(12)
	}
	defer file.Close()
	err = LockExclusive(file)
	if errors.Is(err, ErrFileLockBusy) {
		os.Exit(11)
	}
	if err != nil {
		os.Exit(12)
	}
	UnlockExclusive(file)
	os.Exit(0)
}

func runLockChild(t *testing.T, path string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelper$")
	cmd.Env = append(os.Environ(), "TACET_TEST_LOCK_PATH="+path)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	t.Fatal(err)
	return -1
}

func TestExclusiveLockUsesFileIdentityAndReleasesOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.header")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = LockExclusive(file); err != nil {
		t.Fatal(err)
	}
	if code := runLockChild(t, path); code != 11 {
		t.Fatalf("second process acquired lock: exit=%d", code)
	}
	alias := filepath.Join(dir, "same-header")
	if err = os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if code := runLockChild(t, alias); code != 11 {
		t.Fatalf("hard-link bypassed lock: exit=%d", code)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if code := runLockChild(t, alias); code != 0 {
		t.Fatalf("lock persisted after close: exit=%d", code)
	}
}

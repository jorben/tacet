package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceInfoCountsFilesWithoutFollowingSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bodies"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{"workspace.db": "12345", "bodies/object": "1234567"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "outside"), []byte("not counted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "external")); err != nil {
		t.Fatal(err)
	}
	e := &Engine{opt: Options{Dir: dir}}
	info, err := e.WorkspaceInfo(context.Background())
	if err != nil || info.Dir != dir || info.Bytes != 12 {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestWorkspaceInfoUnavailable(t *testing.T) {
	e := &Engine{opt: Options{Dir: filepath.Join(t.TempDir(), "missing")}}
	if _, err := e.WorkspaceInfo(context.Background()); err == nil || err.Error() != "WORKSPACE_UNAVAILABLE" {
		t.Fatalf("%v", err)
	}
}

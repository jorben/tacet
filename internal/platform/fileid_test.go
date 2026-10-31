//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityFollowsHardLinksAndSymlinksButSeparatesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("x"), 0o600)
	os.WriteFile(b, []byte("x"), 0o600)
	if err := os.Link(a, filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, filepath.Join(dir, "sym")); err != nil {
		t.Fatal(err)
	}
	id := func(n string) FileID {
		v, err := Identify(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !id("a").Same(id("hard")) || !id("a").Same(id("sym")) {
		t.Fatal("an alias of the same file has another identity")
	}
	if id("a").Same(id("b")) {
		t.Fatal("two files share an identity")
	}
	if id("a").Links != 2 || id("b").Links != 1 {
		t.Fatalf("link counts %d %d", id("a").Links, id("b").Links)
	}
	if _, err := Identify(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing path has an identity")
	}
	os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling"))
	if _, err := Identify(filepath.Join(dir, "dangling")); err == nil {
		t.Fatal("a dangling symlink has an identity")
	}
}

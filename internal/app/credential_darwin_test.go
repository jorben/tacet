//go:build darwin

package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jorben/tacet/internal/platform"
	"github.com/jorben/tacet/internal/testenv"
)

// Through the real Keychain, as the desktop app and the CLI open a workspace (no memory key): it is created,
// closed and opened again with the same key. The test's own entry is deleted afterwards. Opt-in, as it
// writes to the login Keychain: TACET_REAL_KEYCHAIN=1.
func TestAWorkspaceOpensWithTheRealCredentialStore(t *testing.T) {
	testenv.UseRealKeychain(t)
	r, _ := root(t)
	t.Setenv("TACET_ROOT", r)
	ws := filepath.Join(t.TempDir(), "ws")
	secret, err := platform.NewSecret(credentialService, credentialAccount("workspace", ws))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secret.Delete() })
	e, err := OpenWith(context.Background(), Config{Workspace: ws})
	if err != nil {
		t.Fatalf("a workspace must open with the platform store: %v", err)
	}
	e.Close()
	if _, err = secret.Load(); err != nil {
		t.Fatalf("the key is in the store: %v", err)
	}
	if e, err = OpenWith(context.Background(), Config{Workspace: ws}); err != nil {
		t.Fatalf("opened again with the stored key: %v", err)
	}
	e.Close()
}

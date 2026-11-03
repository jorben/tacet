//go:build !production

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A development build keeps its workspaces apart from an installed release, so the two never share a
// workspace or the credential entry that unlocks it: the entry's account names the workspace path, and
// TestTheCredentialNameOfAWorkspaceIsAcceptedByTheStore checks that different paths name different entries.
// Downloaded models stay shared: a model is checked against its manifest before use either way.
func TestADevelopmentBuildKeepsItsWorkspacesApartFromTheRelease(t *testing.T) {
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for entry, want := range map[string]string{
		"desktop": filepath.Join(base, "Tacet-dev", "desktop-workspace"),
		"cli":     filepath.Join(base, "Tacet-dev", "cli-workspace"),
	} {
		if got, err := DefaultWorkspace(entry); err != nil || got != want {
			t.Fatalf("%s workspace: %q %v, want %q", entry, got, err, want)
		}
	}
	if got, err := DefaultModelsRoot(); err != nil || got != filepath.Join(base, "Tacet", "models") {
		t.Fatalf("models: %q %v, want the release's folder, shared", got, err)
	}
}

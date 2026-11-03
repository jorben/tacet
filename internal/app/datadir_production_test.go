//go:build production

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAReleaseBuildKeepsItsWorkspacesInTacet(t *testing.T) {
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for entry, want := range map[string]string{
		"desktop": filepath.Join(base, "Tacet", "desktop-workspace"),
		"cli":     filepath.Join(base, "Tacet", "cli-workspace"),
	} {
		if got, err := DefaultWorkspace(entry); err != nil || got != want {
			t.Fatalf("%s workspace: %q %v, want %q", entry, got, err, want)
		}
	}
	if got, err := DefaultModelsRoot(); err != nil || got != filepath.Join(base, "Tacet", "models") {
		t.Fatalf("models: %q %v", got, err)
	}
}

package app

import (
	"strings"
	"testing"

	"github.com/jorben/tacet/internal/platform"
)

// The name every real run uses for its key must be one the platform store accepts: it was once built from
// the workspace path, which the store refuses, so no workspace could open outside tests with a memory key.
func TestTheCredentialNameOfAWorkspaceIsAcceptedByTheStore(t *testing.T) {
	for _, ws := range []string{"/Users/someone/Library/Application Support/Tacet/desktop-workspace", `C:\Users\someone\AppData\Roaming\Tacet\cli-workspace`, "/tmp/x"} {
		acct := credentialAccount("workspace", ws)
		if _, err := platform.NewSecret(credentialService, acct); err != nil {
			t.Fatalf("%q: refused by the store", acct)
		}
		if strings.Contains(acct, "someone") || strings.ContainsAny(acct, `/\:`) {
			t.Fatalf("%q carries the path", acct)
		}
	}
	if credentialAccount("workspace", "/a") == credentialAccount("workspace", "/b") || credentialAccount("workspace", "/a") == credentialAccount("other", "/a") {
		t.Fatal("each workspace and kind has its own entry")
	}
}

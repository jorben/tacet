//go:build !production

package app

// workspaceFolder holds a development build's workspaces, apart from an installed release's: switching
// between the two never opens the other's workspace or asks the credential store for the other's key.
const workspaceFolder = "Tacet-dev"

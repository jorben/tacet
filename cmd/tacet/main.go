// Command tacet is the Tacet CLI. It shares the kernel with the desktop app
// and reaches it only through the app layer (AR-10).
package main

import (
	"os"

	"github.com/jorben/tacet/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}

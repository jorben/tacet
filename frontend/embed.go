// Package frontend embeds the built desktop UI. Run the frontend build (or
// task ensure:dist for a placeholder) before compiling the desktop command.
package frontend

import "embed"

//go:embed all:dist
var Dist embed.FS

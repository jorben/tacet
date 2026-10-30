// Package gui adapts the application to Wails v3. It exposes app commands and
// query reads as bound services, bridges events, and serves preview rasters
// through a custom asset handler with Cache-Control: no-store. It holds no
// business decisions and never exposes plugin methods to the frontend. See
// module design 4.5 (docs/v1/tacet-module-design.md).
package gui

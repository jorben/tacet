// Package platform adapts OS facilities for macOS arm64 and Windows x64:
// sandboxed launch and process trees, workspace locks, exclusive creation and
// no-overwrite rename, key stores, power and wake notifications, private
// directories and file identity (architecture 10). See module design 11.4
// (docs/v1/tacet-module-design.md).
package platform

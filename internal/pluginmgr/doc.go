// Package pluginmgr installs, enables, disables, uninstalls, upgrades and
// rolls back third-party plugins and rule packs (FR-40 to FR-46). Installation
// never runs plugin or dependency build code (SEC-14). See module design 9.3
// (docs/v1/tacet-module-design.md).
package pluginmgr

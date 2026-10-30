// Package snapshot resolves run presets to concrete versions, checks
// compatibility and freezes immutable run snapshots split into facets (MD-09).
// It does not edit configurations (config) or decide which stages rerun (jobs,
// lineage). See module design 5.2 (docs/v1/tacet-module-design.md).
package snapshot

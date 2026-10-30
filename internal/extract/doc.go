// Package extract runs the extraction-and-detection stage. It drives the
// format plugin's inventory and extract streams, stores text units and source
// maps, hands units to detect and commits per unit before granting more stream
// credit. Pipeline does not depend on task state, so validate reuses it on
// candidates. See module design 6.2 (docs/v1/tacet-module-design.md).
package extract

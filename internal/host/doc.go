// Package host runs plugin worker processes: framing and schema checks,
// sessions with flow control, supervision, instance pools and the typed role
// clients used by stage modules. It is the only module that starts plugin
// processes. See module design 10.1 (docs/v1/tacet-module-design.md).
package host

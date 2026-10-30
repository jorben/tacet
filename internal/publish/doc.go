// Package publish owns publish grants bound to resolution keys (MD-16),
// publish intents, no-overwrite publication, delivery records, crash
// reconciliation and superseded versions. It is the only module that writes to
// user-visible locations. See module design 8.4 (docs/v1/tacet-module-
// design.md).
package publish

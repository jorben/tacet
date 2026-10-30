// Package store provides the encrypted workspace and application databases,
// resource files, migrations, transactions with after-commit hooks and the
// outbox, and storage metering. Each module registers and writes only its own
// tables (MD-01). See module design 11.1 (docs/v1/tacet-module-design.md).
package store

// Package rules compiles and matches declarative rules under budgets: RE2
// expressions with boundary attributes, dictionaries and field paths, plus
// registered versioned validators (architecture 8.1). Unsupported syntax or
// exceeded budgets are rejected, never truncated (INV-02). See module design
// 6.4 (docs/v1/tacet-module-design.md).
package rules

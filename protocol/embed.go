// Package protocol embeds the versioned JSON Schema contract. Neither host
// validation nor SDK schema packaging may resolve remote schema references.
package protocol

import "embed"

// Schemas is the sole checked-in contract, also shipped with the Python SDK.
//
//go:embed schema/v1/*.schema.json
var Schemas embed.FS

package store

import "github.com/jorben/tacet/internal/kernel"

// Event is an outbox entry appended inside a transaction and dispatched after
// commit. It carries identifiers and versions only; consumers read details
// from the owner and must be idempotent (MD-05).
type Event struct {
	Type    string
	Subject kernel.ArtifactRef
}

// Tx is a transaction in the workspace or application database.
type Tx interface {
	// AfterCommit registers fn to run once the transaction has committed.
	AfterCommit(fn func())
	// Emit appends an event to the outbox within the transaction.
	Emit(e Event)
}

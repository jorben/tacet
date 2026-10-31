package snapshot

import (
	"encoding/json"
	"errors"

	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/store"
)

var ErrSnapshotMissing = errors.New("SNAPSHOT_NOT_FOUND")
var ErrSnapshotConflict = errors.New("SNAPSHOT_VERSION_CONFLICT")

// Module owns the immutable snapshot content and its facet digests. Registry
// references and batch state are written by their owners in the same transaction.
func Module() store.Module {
	return store.Module{Name: "snapshot", Prefix: "snapshot_", Migrations: []store.Migration{{Version: 1, SQL: []string{
		`CREATE TABLE snapshot_record(batch TEXT NOT NULL, version INTEGER NOT NULL, content BLOB NOT NULL, digests BLOB NOT NULL, PRIMARY KEY(batch,version))`,
	}}}}
}

type Repository struct{}

// Save appends a version with optimistic expectedVersion checking. Frozen
// versions are never updated in place. It uses the caller's workspace transaction
// so snapshot creation and all member transitions can commit or roll back together.
func (Repository) Save(tx store.Tx, batch string, expectedVersion uint64, s *Snapshot) error {
	if batch == "" || len(batch) > 128 || s == nil || s.version == 0 || expectedVersion == ^uint64(0) || s.version != expectedVersion+1 {
		return ErrNotFrozen
	}
	t, err := store.InTransaction(tx)
	if err != nil {
		return err
	}
	scope, err := t.Module("snapshot")
	if err != nil {
		return err
	}
	var current uint64
	if err = t.QueryRow("SELECT COALESCE(MAX(version),0) FROM snapshot_record WHERE batch=?", batch).Scan(&current); err != nil {
		return err
	}
	if current != expectedVersion {
		return ErrSnapshotConflict
	}
	content, err := json.Marshal(s.content)
	if err != nil {
		return ErrNotFrozen
	}
	digests, err := json.Marshal(s.Digests())
	if err != nil {
		return ErrNotFrozen
	}
	if _, err = scope.Exec("INSERT INTO snapshot_record(batch,version,content,digests) VALUES(?,?,?,?)", batch, s.version, content, digests); err != nil {
		return err
	}
	tx.Emit(store.Event{Type: "SnapshotFrozen", Subject: kernel.ArtifactRef{Kind: "snapshot", ID: batch, Version: s.version}})
	return nil
}

// Load verifies the stored content against every facet digest before exposing
// it. Version 0 requests the latest version; a running job should request its
// precise frozen version, never the latest mutable configuration.
func (Repository) Load(tx store.Tx, batch string, version uint64) (*Snapshot, error) {
	t, err := store.InTransaction(tx)
	if err != nil {
		return nil, err
	}
	var content, digests []byte
	if version == 0 {
		err = t.QueryRow("SELECT version,content,digests FROM snapshot_record WHERE batch=? ORDER BY version DESC LIMIT 1", batch).Scan(&version, &content, &digests)
	} else {
		err = t.QueryRow("SELECT content,digests FROM snapshot_record WHERE batch=? AND version=?", batch, version).Scan(&content, &digests)
	}
	if err != nil {
		return nil, ErrSnapshotMissing
	}
	var value Content
	var stored map[kernel.Facet]string
	if json.Unmarshal(content, &value) != nil || json.Unmarshal(digests, &stored) != nil {
		return nil, ErrNotFrozen
	}
	return Restore(version, stored, value)
}

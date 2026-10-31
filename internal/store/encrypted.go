package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum" // registers the "adiantum" VFS
)

var (
	ErrEncryptedOpen = errors.New("STORE_ENCRYPTED_OPEN_FAILED")
	ErrKeyLength     = errors.New("STORE_KEY_LENGTH_INVALID")
)

// KeyLen is the database key size in bytes (architecture 5.1).
const KeyLen = 32

// OpenEncrypted opens (creating if absent) a SQLite database whose main file,
// write-ahead log and rollback journal are encrypted by the Adiantum VFS under
// key. It forces a read of the schema, so a wrong key or a plaintext file at
// the path is refused here rather than at first use.
//
// Every failure is reported as ErrEncryptedOpen with no driver text: the key is
// configured with a PRAGMA, so a driver message could otherwise quote it.
// Adiantum provides confidentiality only; it does not detect modification of
// the file, so backups need their own integrity check.
func OpenEncrypted(ctx context.Context, path string, key []byte) (*sql.DB, error) {
	if len(key) != KeyLen {
		return nil, ErrKeyLength
	}
	if !filepath.IsAbs(path) {
		return nil, ErrEncryptedOpen
	}
	hexKey := hex.EncodeToString(key)
	db, err := driver.Open((&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "vfs=adiantum"}).String(),
		func(c *sqlite3.Conn) error {
			// key first, before any page is read; temp objects stay in memory so
			// no plaintext temporary file is created; deleted pages are overwritten
			return c.Exec("PRAGMA hexkey='" + hexKey + "'; PRAGMA busy_timeout=5000; PRAGMA secure_delete=ON; " +
				"PRAGMA temp_store=MEMORY; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;")
		})
	if err != nil {
		return nil, ErrEncryptedOpen
	}
	db.SetMaxOpenConns(1)
	var n int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&n); err != nil {
		db.Close()
		return nil, ErrEncryptedOpen
	}
	return db, nil
}

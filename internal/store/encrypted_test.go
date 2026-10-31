package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const marker = "TEST_SECRET_MARK_ENC"

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeyLen)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func tctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func fill(t *testing.T, db *sql.DB, rows int) {
	t.Helper()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS t(id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err = tx.Exec("INSERT INTO t(v) VALUES(?)", marker+" row "+strings.Repeat("x", 200)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// scan reports every file in dir that contains the marker or a plain SQLite header.
func scan(t *testing.T, dir string) (files []string, leaks []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, e.Name())
		if bytes.Contains(data, []byte(marker)) || bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
			leaks = append(leaks, e.Name())
		}
	}
	return files, leaks
}

func TestEncryptedFilesHoldNoPlaintextBeforeAndAfterCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.db")
	key := newKey(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db, 3000)
	files, leaks := scan(t, dir) // WAL not yet checkpointed: it holds the pages
	if len(leaks) != 0 {
		t.Fatalf("plaintext in %v (files %v)", leaks, files)
	}
	var wal bool
	for _, f := range files {
		wal = wal || strings.HasSuffix(f, "-wal")
	}
	if !wal {
		t.Fatalf("setup: expected a WAL file, have %v", files)
	}
	if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if _, leaks = scan(t, dir); len(leaks) != 0 {
		t.Fatalf("plaintext after checkpoint in %v", leaks)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, leaks = scan(t, dir); len(leaks) != 0 {
		t.Fatalf("plaintext after close in %v", leaks)
	}
}

func TestEncryptedReopenWithTheSameKeyKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	key := newKey(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db, 500)
	db.Close()
	db, err = OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil || n != 500 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}

func TestWrongKeyAndPlaintextDatabaseAreRefusedWithoutLeakingTheKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.db")
	key := newKey(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db, 50)
	db.Close()
	for name, bad := range map[string][]byte{"wrong key": newKey(t), "all zero key": make([]byte, KeyLen)} {
		if _, err = OpenEncrypted(tctx(t), path, bad); !errors.Is(err, ErrEncryptedOpen) {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(err.Error(), "PRAGMA") || strings.Contains(err.Error(), "hexkey") {
			t.Fatalf("%s: error mentions the key pragma: %v", name, err)
		}
	}
	plain := filepath.Join(dir, "plain.db")
	p, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(plain))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, p, 5)
	p.Close()
	if _, err = OpenEncrypted(tctx(t), plain, key); !errors.Is(err, ErrEncryptedOpen) {
		t.Fatalf("a plaintext database must not be opened as encrypted: %v", err)
	}
}

func TestEncryptedOpenValidatesKeyLengthAndPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	for _, k := range [][]byte{nil, make([]byte, 16), make([]byte, 33)} {
		if _, err := OpenEncrypted(tctx(t), path, k); !errors.Is(err, ErrKeyLength) {
			t.Fatalf("len %d: %v", len(k), err)
		}
	}
	if _, err := OpenEncrypted(tctx(t), "relative.db", newKey(t)); !errors.Is(err, ErrEncryptedOpen) {
		t.Fatalf("relative path: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a rejected key length created a file")
	}
}

func TestEncryptedDatabaseWorksWithMigrationsAndRollsBackFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	db, err := OpenEncrypted(tctx(t), path, newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg, err := NewRegistry(Module{Name: "demo", Prefix: "demo_", Migrations: []Migration{{Version: 1, SQL: []string{
		"CREATE TABLE demo_a(id INTEGER PRIMARY KEY, v TEXT)"}}}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(tctx(t), db, reg, nil)
	if err != nil {
		t.Fatalf("migrate on the encrypted database: %v", err)
	}
	tx, err := st.Begin(tctx(t))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tx.Module("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Exec("INSERT INTO demo_a(v) VALUES('x')"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM demo_a").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback: n=%d err=%v", n, err)
	}
}

func TestEncryptedConnectionKeepsTemporaryDataInMemoryAndOverwritesDeletedPages(t *testing.T) {
	db, err := OpenEncrypted(tctx(t), filepath.Join(t.TempDir(), "ws.db"), newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for pragma, want := range map[string]string{"temp_store": "2", "secure_delete": "1", "journal_mode": "wal", "foreign_keys": "1"} {
		var got string
		if err = db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q (%v), want %q", pragma, got, err, want)
		}
	}
}

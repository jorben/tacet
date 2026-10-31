package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func v1() Module {
	return Module{Name: "demo", Prefix: "demo_", Migrations: []Migration{{Version: 1, SQL: []string{
		"CREATE TABLE demo_a(id INTEGER PRIMARY KEY, v TEXT)"}}}}
}

func v2(sql ...string) Module {
	m := v1()
	m.Migrations = append(m.Migrations, Migration{Version: 2, SQL: sql})
	return m
}

func seeded(t *testing.T) (string, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	path, key := filepath.Join(dir, "ws.db"), newKey(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry(v1())
	if _, err = Open(tctx(t), db, reg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO demo_a(v) VALUES(?)", marker); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return dir, key, path
}

func rows(t *testing.T, path string, key []byte) int {
	t.Helper()
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM demo_a").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFailedMigrationRestoresTheVerifiedBackupOnAnEncryptedDatabase(t *testing.T) {
	dir, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := NewRegistry(v2("CREATE TABLE demo_b(id INTEGER)", "THIS IS NOT SQL"))
	if _, err = Open(tctx(t), db, reg, FileBackup(db, path, key)); !errors.Is(err, ErrMigrationFail) {
		t.Fatalf("migration: %v", err)
	}
	if err = db.Ping(); err == nil {
		t.Fatal("the live handle must be closed before the file is replaced and stays closed after")
	}
	if rows(t, path, key) != 1 {
		t.Fatal("data lost after restore")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*premigrate*"))
	if len(left) != 0 {
		t.Fatalf("backup files left after a completed restore: %v", left)
	}
	if _, leaks := scan(t, dir); len(leaks) != 0 {
		t.Fatalf("plaintext in %v", leaks)
	}
}

func TestSuccessfulMigrationDiscardsTheBackup(t *testing.T) {
	dir, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg, _ := NewRegistry(v2("CREATE TABLE demo_b(id INTEGER)"))
	if _, err = Open(tctx(t), db, reg, FileBackup(db, path, key)); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*premigrate*")); len(left) != 0 {
		t.Fatalf("backup left: %v", left)
	}
}

func TestTamperedBackupNeverReplacesTheDatabase(t *testing.T) {
	_, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	restore, _, err := FileBackup(db, path, key)(tctx(t))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + backupSuffix)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0x01 // flip one bit of ciphertext
	if err = os.WriteFile(path+backupSuffix, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = restore(); !errors.Is(err, ErrBackupCorrupt) {
		t.Fatalf("tampered backup accepted: %v", err)
	}
	if err = db.Ping(); err != nil {
		t.Fatalf("a refused restore must leave the live database open: %v", err)
	}
	db.Close()
	if rows(t, path, key) != 1 {
		t.Fatal("live database changed by a refused restore")
	}
}

func TestMissingMacAndLeftoverBackupAreRefused(t *testing.T) {
	_, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backup := FileBackup(db, path, key)
	restore, _, err := backup(tctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = backup(tctx(t)); !errors.Is(err, ErrBackupFailed) {
		t.Fatalf("a leftover backup must stop a new one: %v", err)
	}
	os.Remove(path + backupSuffix + ".mac")
	if err = restore(); !errors.Is(err, ErrBackupCorrupt) {
		t.Fatalf("a backup without its MAC was accepted: %v", err)
	}
}

func TestBackupMacDependsOnTheKeyAndShortKeysAreRefused(t *testing.T) {
	_, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	macOf := func(k []byte) string {
		_, discard, err := FileBackup(db, path, k)(tctx(t))
		if err != nil {
			t.Fatal(err)
		}
		mac, err := os.ReadFile(path + backupSuffix + ".mac")
		if err != nil || len(mac) == 0 {
			t.Fatalf("mac: %v", err)
		}
		if err = discard(); err != nil {
			t.Fatal(err)
		}
		return string(mac)
	}
	same, other := macOf(key), macOf(newKey(t))
	if same != macOf(key) {
		t.Fatal("the MAC of an unchanged database must be repeatable")
	}
	if same == other {
		t.Fatal("the MAC does not depend on the key, so it could be forged without it")
	}
	if _, _, err = FileBackup(db, path, make([]byte, 5))(tctx(t)); !errors.Is(err, ErrKeyLength) {
		t.Fatalf("short key: %v", err)
	}
}

// crashAfterBackup leaves exactly what a crash during migration leaves: the
// database plus a complete verified backup.
func crashAfterBackup(t *testing.T) (string, []byte) {
	t.Helper()
	_, key, path := seeded(t)
	db, err := OpenEncrypted(tctx(t), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = FileBackup(db, path, key)(tctx(t)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return path, key
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSweepRemovesAStaleBackupWhenTheDatabaseIsIntact(t *testing.T) {
	path, key := crashAfterBackup(t)
	restored, err := SweepBackup(tctx(t), path, key)
	if err != nil || restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if exists(path+backupSuffix) || exists(path+backupSuffix+".mac") {
		t.Fatal("stale backup kept")
	}
	if rows(t, path, key) != 1 {
		t.Fatal("database changed")
	}
}

func TestSweepRestoresAVerifiedBackupWhenTheDatabaseIsDamaged(t *testing.T) {
	path, key := crashAfterBackup(t)
	if err := os.WriteFile(path, []byte("garbage that is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+"-wal", []byte("stale log"), 0o600)
	restored, err := SweepBackup(tctx(t), path, key)
	if err != nil || !restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if rows(t, path, key) != 1 || exists(path+"-wal") || exists(path+backupSuffix) {
		t.Fatal("restore incomplete")
	}
}

func TestSweepRestoresWhenTheDatabaseFileIsGone(t *testing.T) {
	path, key := crashAfterBackup(t)
	os.Remove(path)
	if restored, err := SweepBackup(tctx(t), path, key); err != nil || !restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if rows(t, path, key) != 1 {
		t.Fatal("data not restored")
	}
}

func TestSweepNeverUsesATamperedBackupAndLeavesEverythingForTheUser(t *testing.T) {
	path, key := crashAfterBackup(t)
	damaged := []byte("garbage that is not a database")
	os.WriteFile(path, damaged, 0o600)
	data, _ := os.ReadFile(path + backupSuffix)
	data[len(data)/3] ^= 0x01
	os.WriteFile(path+backupSuffix, data, 0o600)
	if _, err := SweepBackup(tctx(t), path, key); !errors.Is(err, ErrBackupCorrupt) {
		t.Fatalf("tampered backup: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(damaged) {
		t.Fatal("main file replaced from an unverified backup")
	}
	if !exists(path + backupSuffix) {
		t.Fatal("the only remaining copy was deleted")
	}
}

func TestSweepClearsTemporaryPiecesWithoutACompleteCopyAndChecksKey(t *testing.T) {
	_, key, path := seeded(t)
	os.WriteFile(path+backupSuffix+".tmp", []byte("partial"), 0o600)
	os.WriteFile(path+backupSuffix+".mac", []byte("x"), 0o600)
	if restored, err := SweepBackup(tctx(t), path, key); err != nil || restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	if exists(path+backupSuffix+".tmp") || exists(path+backupSuffix+".mac") || rows(t, path, key) != 1 {
		t.Fatal("pieces left or database changed")
	}
	if _, err := SweepBackup(tctx(t), path, make([]byte, 3)); !errors.Is(err, ErrKeyLength) {
		t.Fatalf("short key: %v", err)
	}
}

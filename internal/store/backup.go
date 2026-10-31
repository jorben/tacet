package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrBackupCorrupt = errors.New("STORE_BACKUP_CORRUPT")

const backupSuffix = ".premigrate"

// FileBackup is the Backup used with an encrypted database at path. It
// checkpoints the log and copies the (already encrypted) main file next to it,
// with an HMAC over the copy: Adiantum does not detect modification, so the
// copy must carry its own integrity check before it may replace the database.
//
// restore closes db, verifies the HMAC, replaces the main file atomically and
// removes the stale log; the caller must reopen. An existing leftover backup
// stops a new one, because it may be the only good copy.
func FileBackup(db *sql.DB, path string, key []byte) Backup {
	copyPath, macPath := path+backupSuffix, path+backupSuffix+".mac"
	derived := backupMacKey(key)
	sum := func(file string) (string, error) { return fileMAC(derived, file) }
	remove := func() error { return removeBackup(path) }
	return func(ctx context.Context) (func() error, func() error, error) {
		if len(key) != KeyLen {
			return nil, nil, ErrKeyLength
		}
		for _, f := range []string{copyPath, macPath} {
			if _, err := os.Stat(f); err == nil {
				return nil, nil, ErrBackupFailed
			}
		}
		if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return nil, nil, ErrBackupFailed
		}
		if err := copyFile(path, copyPath+".tmp"); err != nil {
			remove()
			return nil, nil, ErrBackupFailed
		}
		mac, err := sum(copyPath + ".tmp")
		if err != nil || os.WriteFile(macPath, []byte(mac), 0o600) != nil || os.Rename(copyPath+".tmp", copyPath) != nil {
			remove()
			return nil, nil, ErrBackupFailed
		}
		restore := func() error {
			want, err := os.ReadFile(macPath)
			if err != nil {
				return ErrBackupCorrupt
			}
			got, err := sum(copyPath)
			if err != nil || !hmac.Equal([]byte(strings.TrimSpace(string(want))), []byte(got)) {
				return ErrBackupCorrupt // a tampered copy never replaces the database
			}
			if err = db.Close(); err != nil {
				return ErrBackupFailed
			}
			if err = copyFile(copyPath, path+".restoring"); err != nil || os.Rename(path+".restoring", path) != nil {
				os.Remove(path + ".restoring")
				return ErrBackupFailed
			}
			os.Remove(path + "-wal")
			os.Remove(path + "-shm")
			return remove()
		}
		return restore, remove, nil
	}
}

func backupMacKey(key []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("tacet/backup-mac/v1"))
	return h.Sum(nil)
}

func fileMAC(derived []byte, file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := hmac.New(sha256.New, derived)
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeBackup(path string) error {
	var first error
	for _, f := range []string{path + backupSuffix, path + backupSuffix + ".mac", path + backupSuffix + ".tmp"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

// SweepBackup settles a backup left behind by a crash (or a failed discard),
// before the database is opened. Migration is one transaction, so the main
// file is either the old state or the new one, never half-migrated: if it opens
// with the key, the leftover is stale and removed. If it does not open, the
// backup is restored, but only after its HMAC verifies; a tampered or
// incomplete backup is never used and the main file is left as it is.
// restored reports whether the main file was replaced.
func SweepBackup(ctx context.Context, path string, key []byte) (restored bool, err error) {
	if len(key) != KeyLen {
		return false, ErrKeyLength
	}
	copyPath, macPath := path+backupSuffix, path+backupSuffix+".mac"
	if _, serr := os.Stat(copyPath); serr != nil {
		// no complete copy: only temporary pieces can exist, and they are useless
		return false, removeBackup(path)
	}
	if _, serr := os.Stat(path); serr == nil {
		if db, oerr := OpenEncrypted(ctx, path, key); oerr == nil {
			db.Close()
			return false, removeBackup(path)
		}
	}
	want, rerr := os.ReadFile(macPath)
	got, merr := fileMAC(backupMacKey(key), copyPath)
	if rerr != nil || merr != nil || !hmac.Equal([]byte(strings.TrimSpace(string(want))), []byte(got)) {
		return false, ErrBackupCorrupt
	}
	if err = copyFile(copyPath, path+".restoring"); err != nil || os.Rename(path+".restoring", path) != nil {
		os.Remove(path + ".restoring")
		return false, ErrBackupFailed
	}
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
	return true, removeBackup(path)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

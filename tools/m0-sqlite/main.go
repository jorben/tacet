// M0 only: compare an explicitly permitted encryption candidate under a fixed
// synthetic 100k-finding workload. Not imported by the application kernel.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
)

const marker = "M0_SYNTHETIC_PRIVATE_MARKER_83CE9"

func main() {
	report := flag.String("report", "", "tracked non-sensitive JSON report path")
	flag.Parse()
	if *report == "" {
		panic("M0_REPORT_REQUIRED")
	}
	if err := run(*report); err != nil {
		fmt.Fprintln(os.Stderr, "M0_SQLITE_PROBE_FAILED")
		os.Exit(1)
	}
}

func run(report string) error {
	dir, err := os.MkdirTemp("", "tacet-m0-db-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	defer clear(key)
	path := filepath.Join(dir, "findings.db")
	// The callback configures the key before any schema access. Do not put
	// key material in a URI, output report or error/diagnostic string.
	open := func(key []byte) (*sql.DB, error) {
		db, err := driver.Open("file:"+filepath.ToSlash(path)+"?vfs=adiantum", func(c *sqlite3.Conn) error {
			return c.Exec("PRAGMA hexkey='" + hex.EncodeToString(key) + "'; PRAGMA busy_timeout=5000; PRAGMA secure_delete=ON; PRAGMA temp_store=MEMORY;")
		})
		if err == nil {
			db.SetMaxOpenConns(1)
		}
		return db, err
	}
	started := time.Now()
	db, err := open(key)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
CREATE TABLE findings (id INTEGER PRIMARY KEY, job INTEGER NOT NULL, category INTEGER NOT NULL, position INTEGER NOT NULL, value_hmac BLOB NOT NULL, synthetic_marker TEXT NOT NULL);
CREATE INDEX findings_filter ON findings(job,category,id);
CREATE INDEX findings_groups ON findings(category,value_hmac);`); err != nil {
		return err
	}
	opened := time.Now()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO findings VALUES(?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	for i := 1; i <= 100000; i++ {
		group := make([]byte, 32)
		group[0] = byte(i % 251)
		group[1] = byte(i % 127)
		if _, err = stmt.Exec(i, i%200, i%10, i*7, group, marker); err != nil {
			return err
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		return err
	}
	inserted := time.Now()
	query := func(statement string, args ...any) (time.Duration, int, error) {
		start := time.Now()
		rows, err := db.QueryContext(context.Background(), statement, args...)
		if err != nil {
			return 0, 0, err
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			return 0, 0, err
		}
		count := 0
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				return 0, 0, err
			}
			count++
		}
		return time.Since(start), count, rows.Err()
	}
	first, n, err := query("SELECT id,position FROM findings WHERE job=? AND category=? AND id>? ORDER BY id LIMIT 100", 7, 7, 0)
	if err != nil || n != 100 {
		return fmt.Errorf("M0_QUERY_FAILED")
	}
	grouped, _, err := query("SELECT category,value_hmac,COUNT(*) FROM findings GROUP BY category,value_hmac LIMIT 100")
	if err != nil {
		return err
	}
	files := map[string]int64{}
	for _, name := range []string{"findings.db", "findings.db-wal", "findings.db-shm"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(marker)) || bytes.HasPrefix(data, []byte("SQLite format 3")) {
			return fmt.Errorf("M0_PLAINTEXT_FOUND")
		}
		files[name] = int64(len(data))
	}
	// Use SQLite's checkpoint before close; a raw copy of an active WAL DB
	// would not be a valid migration backup. Production backup comes later.
	if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	wrong, err := open(bytes.Repeat([]byte{0xAA}, 32))
	if err != nil {
		return err
	}
	var count int
	err = wrong.QueryRow("SELECT COUNT(*) FROM findings").Scan(&count)
	wrong.Close()
	if err == nil {
		return fmt.Errorf("M0_WRONG_KEY_ACCEPTED")
	}
	reopened, err := open(key)
	if err != nil {
		return err
	}
	defer reopened.Close()
	if err = reopened.QueryRow("SELECT COUNT(*) FROM findings").Scan(&count); err != nil || count != 100000 {
		return fmt.Errorf("M0_REOPEN_FAILED")
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := map[string]any{
		"probe": "m0-adiantum-sqlite-100k", "status": "passed", "rows": count,
		"go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
		"seconds":                              map[string]float64{"open_schema": opened.Sub(started).Seconds(), "insert_100k": inserted.Sub(opened).Seconds(), "filter_first_100": first.Seconds(), "group_first_100": grouped.Seconds()},
		"files_without_marker_or_plain_header": files, "reopen_count": count, "wrong_key_rejected": true,
		"go_heap_alloc_bytes": memory.HeapAlloc, "go_heap_sys_bytes": memory.HeapSys,
		"limitations": []string{"one synthetic run on development machine, not R1", "Adiantum provides confidentiality, no tamper authentication", "shm contains indexing metadata, not a claim of encrypted shm", "not module-owned schema/outbox/migration implementation", "not Windows verification"},
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(report), 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(report, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

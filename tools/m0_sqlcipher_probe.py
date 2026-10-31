#!/usr/bin/env python3
"""M0 SQLCipher comparison via ctypes, key API (never CLI arguments/SQL URI).

The synthetic workload matches tools/m0-sqlite. Uses an installed candidate
library only for measurement; production driver/build remains a later decision.
"""
from __future__ import annotations

import argparse
import ctypes as c
import ctypes.util
import json
import os
import platform
import tempfile
import time
from pathlib import Path

MARKER = 'M0_SYNTHETIC_PRIVATE_MARKER_83CE9'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--library', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    lib = c.CDLL(str(args.library))
    lib.sqlite3_open.argtypes = [c.c_char_p, c.POINTER(c.c_void_p)]
    lib.sqlite3_open.restype = c.c_int
    lib.sqlite3_key.argtypes = [c.c_void_p, c.c_void_p, c.c_int]
    lib.sqlite3_key.restype = c.c_int
    lib.sqlite3_exec.argtypes = [c.c_void_p, c.c_char_p, c.c_void_p, c.c_void_p, c.c_void_p]
    lib.sqlite3_exec.restype = c.c_int
    lib.sqlite3_close.argtypes = [c.c_void_p]
    lib.sqlite3_close.restype = c.c_int
    lib.sqlite3_prepare_v2.argtypes = [c.c_void_p, c.c_char_p, c.c_int, c.POINTER(c.c_void_p), c.c_void_p]
    lib.sqlite3_prepare_v2.restype = c.c_int
    lib.sqlite3_step.argtypes = [c.c_void_p]
    lib.sqlite3_step.restype = c.c_int
    lib.sqlite3_finalize.argtypes = [c.c_void_p]
    lib.sqlite3_bind_int.argtypes = [c.c_void_p, c.c_int, c.c_int]
    lib.sqlite3_bind_blob.argtypes = [c.c_void_p, c.c_int, c.c_void_p, c.c_int, c.c_void_p]
    lib.sqlite3_reset.argtypes = [c.c_void_p]
    lib.sqlite3_clear_bindings.argtypes = [c.c_void_p]
    lib.sqlite3_column_int.argtypes = [c.c_void_p, c.c_int]
    lib.sqlite3_column_int.restype = c.c_int
    lib.sqlite3_memory_used.restype = c.c_int64

    def check(code):
        if code != 0:
            raise RuntimeError('M0_SQLCIPHER_CALL_FAILED')

    def execute(db, sql):
        check(lib.sqlite3_exec(db, sql.encode(), None, None, None))

    def open_db(path, key):
        db = c.c_void_p()
        check(lib.sqlite3_open(os.fsencode(path), c.byref(db)))
        try:
            check(lib.sqlite3_key(db, key, len(key)))
            execute(db, 'PRAGMA secure_delete=ON; PRAGMA temp_store=MEMORY; PRAGMA busy_timeout=5000;')
        except Exception:
            lib.sqlite3_close(db)
            raise
        return db

    def prepare(db, sql):
        statement = c.c_void_p()
        check(lib.sqlite3_prepare_v2(db, sql.encode(), -1, c.byref(statement), None))
        return statement

    def query(db, sql):
        start = time.monotonic()
        stmt = prepare(db, sql)
        count, first = 0, None
        try:
            while (code := lib.sqlite3_step(stmt)) == 100:
                count += 1
                if first is None:
                    first = lib.sqlite3_column_int(stmt, 0)
            if code != 101:
                raise RuntimeError('M0_SQLCIPHER_QUERY_FAILED')
        finally:
            lib.sqlite3_finalize(stmt)
        return time.monotonic() - start, count, first

    with tempfile.TemporaryDirectory(prefix='tacet-m0-sqlcipher-') as directory:
        path = Path(directory) / 'findings.db'
        key = os.urandom(32)
        start = time.monotonic()
        db = open_db(path, key)
        try:
            execute(db, '''PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
CREATE TABLE findings (id INTEGER PRIMARY KEY, job INTEGER NOT NULL, category INTEGER NOT NULL, position INTEGER NOT NULL, value_hmac BLOB NOT NULL, synthetic_marker TEXT NOT NULL);
CREATE INDEX findings_filter ON findings(job,category,id);
CREATE INDEX findings_groups ON findings(category,value_hmac);''')
            opened = time.monotonic()
            execute(db, 'BEGIN IMMEDIATE')
            stmt = prepare(db, f"INSERT INTO findings VALUES(?,?,?,?,?,'{MARKER}')")
            try:
                for i in range(1, 100001):
                    for index, value in enumerate((i, i % 200, i % 10, i * 7), 1):
                        check(lib.sqlite3_bind_int(stmt, index, value))
                    group = bytes([i % 251, i % 127]) + bytes(30)
                    check(lib.sqlite3_bind_blob(stmt, 5, group, 32, c.c_void_p(-1)))
                    if lib.sqlite3_step(stmt) != 101:
                        raise RuntimeError('M0_SQLCIPHER_INSERT_FAILED')
                    check(lib.sqlite3_reset(stmt))
                    check(lib.sqlite3_clear_bindings(stmt))
            finally:
                lib.sqlite3_finalize(stmt)
            execute(db, 'COMMIT')
            inserted = time.monotonic()
            first, count, _ = query(db, 'SELECT id,position FROM findings WHERE job=7 AND category=7 AND id>0 ORDER BY id LIMIT 100')
            if count != 100:
                raise RuntimeError('M0_SQLCIPHER_COUNT_MISMATCH')
            grouped, _, _ = query(db, 'SELECT category,value_hmac,COUNT(*) FROM findings GROUP BY category,value_hmac LIMIT 100')
            files = {}
            for name in ('findings.db', 'findings.db-wal', 'findings.db-shm'):
                content = (Path(directory) / name).read_bytes()
                if MARKER.encode() in content or content.startswith(b'SQLite format 3'):
                    raise RuntimeError('M0_SQLCIPHER_PLAINTEXT_FOUND')
                files[name] = len(content)
            memory = lib.sqlite3_memory_used()
            execute(db, 'PRAGMA wal_checkpoint(TRUNCATE)')
        finally:
            check(lib.sqlite3_close(db))
        db = open_db(path, b'X' * 32)
        try:
            try:
                query(db, 'SELECT COUNT(*) FROM findings')
            except RuntimeError:
                wrong_key_rejected = True
            else:
                raise RuntimeError('M0_SQLCIPHER_WRONG_KEY_ACCEPTED')
        finally:
            lib.sqlite3_close(db)
        db = open_db(path, key)
        try:
            _, _, rows = query(db, 'SELECT COUNT(*) FROM findings')
        finally:
            lib.sqlite3_close(db)
        if rows != 100000:
            raise RuntimeError('M0_SQLCIPHER_REOPEN_FAILED')
    report = {'probe': 'm0-sqlcipher-100k', 'status': 'passed', 'rows': rows,
              'platform': platform.platform(), 'seconds': {'open_schema': opened-start,
              'insert_100k': inserted-opened, 'filter_first_100': first, 'group_first_100': grouped},
              'files_without_marker_or_plain_header': files, 'wrong_key_rejected': wrong_key_rejected,
              'sqlite_memory_used_bytes': memory,
              'limitations': ['one synthetic development-machine run, not R1',
                              'ctypes SQLCipher C API vs Go WASM driver timings include different binding overhead',
                              'not production Go driver, cross-platform build or migration evidence',
                              'shm metadata is not claimed encrypted']}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    main()

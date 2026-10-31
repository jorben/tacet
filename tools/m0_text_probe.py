#!/usr/bin/env python3
"""Measure the TXT and CSV streaming scanners and rewrite on generated files.

Run with python/.venv/bin/python, one shape per process so peak RSS is
comparable. Inputs are generated synthetic markers, removed afterward. Single
run on the development machine: not R1, not a distribution, not an admission.
"""
from __future__ import annotations

import argparse
import faulthandler
import json
import platform
import resource
import sys
import tempfile
import time
from pathlib import Path

from tacet_format_csv import scanner as csv_sc
from tacet_format_text import scanner as txt_sc

MIB = 1 << 20
MARKER = 'TEST_SECRET_MARK'


def generate(path, shape, target):
    with open(path, 'wb') as out:
        if shape == 'text':
            line = ('2026-01-01 INFO 联系 ' + MARKER + ' 结束 public 普通内容\r\n').encode()
            for _ in range(max(1, target // len(line))):
                out.write(line)
        elif shape == 'longline':  # one line, no newline
            unit = ('public 普通内容 ' * 64 + MARKER + ' ').encode()
            for _ in range(max(1, target // len(unit))):
                out.write(unit)
        elif shape == 'csv':
            out.write('id,name,note\r\n'.encode())
            row = ('001,"Zhang, San","联系 ' + MARKER + ' ""q"""\r\n').encode()
            for _ in range(max(1, target // len(row))):
                out.write(row)
        else:
            raise SystemExit(f'unknown shape {shape}')


def text_edits(chunks):
    """Edits for every marker, including markers split across chunk boundaries."""
    chunks = list(chunks) if False else chunks
    carry_chunks, carry = [], ''  # (chunk, start index in chunk) pieces of the carried tail
    base = 0
    previous = None
    for chunk in chunks:
        text = chunk.text
        start = 0
        if previous is not None:
            tail = previous.text[-(len(MARKER) - 1):]
            for keep in range(len(tail), 0, -1):
                need = len(MARKER) - keep
                if tail[-keep:] == MARKER[:keep] and text[:need] == MARKER[keep:]:
                    yield txt_sc.range_edit(previous, len(previous.text) - keep, len(previous.text), '[MARK]')
                    yield txt_sc.range_edit(chunk, 0, need, '')
                    start = need
                    break
        index = text.find(MARKER, start)
        while index >= 0:
            yield txt_sc.range_edit(chunk, index, index + len(MARKER), '[MARK]')
            index = text.find(MARKER, index + len(MARKER))
        previous = chunk


def csv_edits(events):
    dialect = csv_sc.Dialect()
    for event in events:
        if isinstance(event, csv_sc.Cell) and MARKER in event.text:
            yield csv_sc.cell_edit(event, event.text.replace(MARKER, '[MARK]'), dialect)


def count_text(chunks):
    remaining = replaced = 0
    carry = ''
    for chunk in chunks:
        joined = carry + chunk.text
        remaining += joined.count(MARKER) - carry.count(MARKER)
        replaced += joined.count('[MARK]') - carry.count('[MARK]')
        carry = joined[-(len(MARKER) - 1):]
    return remaining, replaced


def count_csv(events):
    remaining = replaced = records = 0
    for event in events:
        if isinstance(event, csv_sc.Cell):
            remaining += event.text.count(MARKER)
            replaced += event.text.count('[MARK]')
        else:
            records += 1
    return remaining, replaced, records


def main():
    faulthandler.dump_traceback_later(1500, exit=True)  # hard stop: never hang the caller
    parser = argparse.ArgumentParser()
    parser.add_argument('--shape', choices=['text', 'longline', 'csv'], required=True)
    parser.add_argument('--mib', type=int, required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    is_csv = args.shape == 'csv'
    result = {'shape': args.shape, 'requested_mib': args.mib, 'python': platform.python_version(),
              'platform': platform.platform()}
    with tempfile.TemporaryDirectory(prefix='tacet-text-probe-') as work:
        source, output = Path(work, 'in'), Path(work, 'out')
        generate(source, args.shape, args.mib * MIB)
        result['source_bytes'] = source.stat().st_size

        def events(path):
            with open(path, 'rb') as handle:
                yield from (csv_sc.scan(handle) if is_csv else txt_sc.scan(handle))

        t0 = time.perf_counter()
        n = sum(1 for _ in events(source))
        result['scan_seconds'] = round(time.perf_counter() - t0, 3)
        result['events'] = n

        t0 = time.perf_counter()
        with open(source, 'rb') as first, open(source, 'rb') as second, open(output, 'wb') as sink:
            if is_csv:
                csv_sc.rewrite(second, sink, csv_edits(csv_sc.scan(first)))
            else:
                txt_sc.rewrite(second, sink, text_edits(txt_sc.scan(first)))
        result['rewrite_seconds'] = round(time.perf_counter() - t0, 3)
        result['output_bytes'] = output.stat().st_size

        t0 = time.perf_counter()
        if is_csv:
            remaining, replaced, records = count_csv(events(output))
            result['records'] = records
        else:
            remaining, replaced = count_text(events(output))
        result['verify_seconds'] = round(time.perf_counter() - t0, 3)
        result['remaining_markers'] = remaining
        result['replaced_markers'] = replaced
    peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    result['peak_rss_bytes'] = peak if sys.platform == 'darwin' else peak * 1024
    result['note'] = 'single run; development machine; not R1; not a distribution'
    Path(args.report).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if remaining:
        raise SystemExit('markers remained in output')


if __name__ == '__main__':
    main()

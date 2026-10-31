#!/usr/bin/env python3
"""Measure the streaming JSON/JSONL scanner and rewrite on generated files.

Run with python/.venv/bin/python, one shape per process so peak RSS is
comparable. Inputs are generated synthetic markers only and removed afterward.
This is a single-run prototype measurement on the development machine: it is
not an R1 result, a percentile distribution or a format admission.
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import resource
import sys
import tempfile
import time
from pathlib import Path

from tacet_format_json import scanner as sc

MIB = 1 << 20
MARKER = 'TEST_SECRET_MARK'


def generate(path, shape, target):
    with open(path, 'wb') as out:
        if shape == 'array':
            record = ('{"id":12345678901234567890123,"score":1.50e+2,"note":"普通内容 public '
                      + MARKER + ' text","tags":["a","b"],"amount":10.10}').encode()
            out.write(b'[')
            count = max(1, target // (len(record) + 1))
            for i in range(count):
                out.write((b',' if i else b'') + record)
            out.write(b']')
        elif shape == 'jsonl':
            record = ('{"id":12345678901234567890123,"msg":"联系 ' + MARKER + ' 结束","v":1.0}\n').encode()
            for _ in range(max(1, target // len(record))):
                out.write(record)
        elif shape == 'string':
            out.write(b'{"body":"')
            unit = ('public 普通内容 ' * 64 + MARKER + ' ').encode()
            for _ in range(max(1, target // len(unit))):
                out.write(unit)
            out.write(b'"}')
        else:
            raise SystemExit(f'unknown shape {shape}')


def edits_for(events, limit_every):
    """Range edits for every marker, including markers split across two chunks.

    Chunk boundaries are an implementation detail, so a marker may straddle two
    chunks (the production host overlaps chunks for the same reason). The head
    part is replaced in the earlier chunk and the rest removed in the next one.
    """
    previous = None
    for event in events:
        if event.kind == sc.STRING_BEGIN:
            previous = None
        if event.kind == sc.STRING_CHUNK:
            start = 0
            if previous is not None:
                tail = previous.text[-(len(MARKER) - 1):]
                for keep in range(len(tail), 0, -1):  # longest suffix that begins the marker
                    need = len(MARKER) - keep
                    if tail[-keep:] == MARKER[:keep] and event.text[:need] == MARKER[keep:]:
                        yield sc.range_edit(previous, len(previous.text) - keep, len(previous.text), '[MARK]')
                        yield sc.range_edit(event, 0, need, '')
                        start = need
                        break
            index = event.text.find(MARKER, start)
            while index >= 0:
                yield sc.range_edit(event, index, index + len(MARKER), '[MARK]')
                index = event.text.find(MARKER, index + len(MARKER))
            previous = event
        elif event.kind == sc.NUMBER and event.text == '12345678901234567890123':
            yield sc.Edit(event.start, event.end, b'null')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--shape', choices=['array', 'jsonl', 'string'], required=True)
    parser.add_argument('--mib', type=int, required=True)
    parser.add_argument('--report', required=True)
    args = parser.parse_args()
    limits = sc.Limits()
    result = {'shape': args.shape, 'requested_mib': args.mib, 'python': platform.python_version(),
              'platform': platform.platform(), 'jsonl': args.shape == 'jsonl'}
    with tempfile.TemporaryDirectory(prefix='tacet-json-probe-') as work:
        source, output = Path(work, 'in'), Path(work, 'out')
        t0 = time.perf_counter()
        generate(source, args.shape, args.mib * MIB)
        result['source_bytes'] = source.stat().st_size
        result['generate_seconds'] = round(time.perf_counter() - t0, 3)

        t0 = time.perf_counter()
        counts = {'events': 0, 'numbers': 0, 'string_chunks': 0}
        with open(source, 'rb') as handle:
            for event in sc.scan(handle, limits, jsonl=args.shape == 'jsonl'):
                counts['events'] += 1
                counts['numbers'] += event.kind == sc.NUMBER
                counts['string_chunks'] += event.kind == sc.STRING_CHUNK
        result['scan_seconds'] = round(time.perf_counter() - t0, 3)
        result.update(counts)

        t0 = time.perf_counter()
        with open(source, 'rb') as first, open(source, 'rb') as second, open(output, 'wb') as sink:
            edits = edits_for(sc.scan(first, limits, jsonl=args.shape == 'jsonl'), 1)
            sc.rewrite(second, sink, edits)
        result['rewrite_seconds'] = round(time.perf_counter() - t0, 3)
        result['output_bytes'] = output.stat().st_size

        t0 = time.perf_counter()
        remaining = markers = nulls = 0
        carry = ''
        with open(output, 'rb') as handle:
            for event in sc.scan(handle, limits, jsonl=args.shape == 'jsonl'):
                if event.kind == sc.STRING_BEGIN:
                    carry = ''
                elif event.kind == sc.STRING_CHUNK:
                    joined = carry + event.text
                    # matches wholly inside the old carry were counted with the previous chunk
                    remaining += joined.count(MARKER) - carry.count(MARKER)
                    markers += joined.count('[MARK]') - carry.count('[MARK]')
                    carry = joined[-(len(MARKER) - 1):]
                elif event.kind == sc.LITERAL and event.text == 'null':
                    nulls += 1
                elif event.kind == sc.NUMBER and event.text == '1.50e+2':
                    remaining += 0
        result['verify_seconds'] = round(time.perf_counter() - t0, 3)
        result['remaining_markers'] = remaining
        result['replaced_markers'] = markers
        result['nulls'] = nulls
        result['output_reparses'] = True  # the verification scan above raised nothing
    peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    result['peak_rss_bytes'] = peak if sys.platform == 'darwin' else peak * 1024
    result['note'] = 'single run; development machine; not R1; not a distribution'
    Path(args.report).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if remaining:
        raise SystemExit('markers remained in output')


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Generate a synthetic DOCX subset and measure real package rewrite/scan.

Run with python/.venv/bin/python. Each invocation is a fresh process so peak
RSS can be compared. Temporary files contain only generated test markers.
This does not establish format admission, Word/WPS compatibility or R1 speed.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import resource
import sys
import tempfile
import time
import zipfile
from pathlib import Path

from tacet_format_docx.package import inventory, rewrite_text_subset
from tacet_format_docx.xmlparts import Edit, scan
from tacet_plugin_sdk.safezip import Archive

W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
CT = 'http://schemas.openxmlformats.org/package/2006/content-types'
REL = 'http://schemas.openxmlformats.org/package/2006/relationships'


def generate(path, target_bytes):
    paragraph = ('<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>PUBLIC START TEST_SEC'
                 '</w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t>RET_MARKER '
                 + '普通内容 public data ' * 100 + '</w:t></w:r></w:p>').encode()
    with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_STORED, allowZip64=False) as archive:
        archive.writestr('[Content_Types].xml', f'<Types xmlns="{CT}"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
        archive.writestr('_rels/.rels', f'<Relationships xmlns="{REL}"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
        with archive.open('word/document.xml', 'w') as document:
            document.write(f'<w:document xmlns:w="{W}"><w:body>'.encode())
            count = max(1, target_bytes // len(paragraph))
            for _ in range(count):
                document.write(paragraph)
            document.write(b'</w:body></w:document>')
    return count


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mib', type=int, default=1)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    if not 1 <= args.mib <= 100:
        parser.error('probe size must be between 1 and 100 MiB')
    marker = 'TEST_SECRET_MARKER'
    start = time.monotonic()
    with tempfile.TemporaryDirectory(prefix='tacet-m0-synthetic-') as directory:
        os.chmod(directory, 0o700)
        source_path = Path(directory) / 'source.docx'
        candidate_path = Path(directory) / 'candidate.docx'
        count = generate(source_path, args.mib * 1024**2)
        generated = time.monotonic()
        def decide(name, paragraph):
            position = paragraph.text.index(marker)
            return [Edit(position, position + len(marker), '[REDACTED]')]
        with source_path.open('rb') as source, candidate_path.open('wb') as candidate:
            before = rewrite_text_subset(source, candidate, decide)
        written = time.monotonic()
        seen = 0
        def verify(paragraph):
            nonlocal seen
            if marker in paragraph.text or '[REDACTED]' not in paragraph.text:
                raise RuntimeError('PROBE_OUTPUT_MISMATCH')
            if not paragraph.text.startswith('PUBLIC START [REDACTED]'):
                raise RuntimeError('PROBE_SOURCE_MAPPING_MISMATCH')
            seen += 1
        with candidate_path.open('rb') as candidate:
            archive = Archive(candidate)
            try:
                after = inventory(archive)
                with archive.open('word/document.xml') as part:
                    scan(part, verify)
            finally:
                archive.close()
        verified = time.monotonic()
        if seen != count or [p.name for p in before.parts] != [p.name for p in after.parts]:
            raise RuntimeError('PROBE_INVENTORY_MISMATCH')
        digest = hashlib.file_digest(candidate_path.open('rb'), 'sha256').hexdigest()
        report = {
            'probe': 'm0-docx-text-subset', 'status': 'passed',
            'command': f'python/.venv/bin/python tools/m0_docx_probe.py --mib {args.mib} --report <report>',
            'environment': {'system': platform.system(), 'release': platform.release(),
                            'machine': platform.machine(), 'python': platform.python_version()},
            'requested_mib': args.mib, 'input_bytes': source_path.stat().st_size,
            'candidate_bytes': candidate_path.stat().st_size, 'paragraphs': seen,
            'input_parts': len(before.parts), 'candidate_sha256': digest,
            'seconds': {'generate': generated-start, 'rewrite': written-generated,
                        'reopen_inventory_scan': verified-written, 'total': verified-start},
            'peak_rss_bytes': resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if sys.platform == 'darwin' else 1024),
            'limitations': ['synthetic text-only OPC subset', 'development machine, not R1',
                            'no model, sandbox, encrypted resource or Wails pipeline',
                            'not Word/WPS compatibility evidence', 'not format admission'],
        }
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))


if __name__ == '__main__':
    main()

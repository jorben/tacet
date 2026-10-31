#!/usr/bin/env python3
"""Verify real Go/Python cross-process codecs and encrypted DOCX stream I/O.

All input is generated; a test key is passed only on stdin. Production workers
must use inherited OS handles or host data proxies, not these probe file paths.
"""
import argparse
import io
import json
import os
import subprocess
import tempfile
from pathlib import Path

from tacet_plugin_sdk.resources import ResourceReader, ResourceWriter
from tacet_format_docx.package import rewrite_text_subset
from tacet_format_docx.xmlparts import Edit, scan
from tacet_plugin_sdk.safezip import Archive

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='tacet-interop-generated-') as directory:
        root = Path(directory)
        binary = root / 'probe'
        subprocess.run(['go', 'build', '-o', str(binary), './tools/m0-resource-interop'], cwd=ROOT, check=True, capture_output=True)
        key = os.urandom(32)
        secret = key.hex().encode() + b'\n'
        original = (b'generated-multi-segment-marker' * 10_000) + '中文😀'.encode()
        source = root / 'source'
        source.write_bytes(original)
        go_resource = root / 'go-resource'
        subprocess.run([str(binary), '--mode', 'encode', '--input', str(source), '--output', str(go_resource)],
                       input=secret, check=True, capture_output=True)
        with go_resource.open('rb') as handle:
            reader = ResourceReader(handle, key, 'm0-cross-process')
            reader.verify()
            result = bytearray()
            while chunk := reader.read(64 * 1024):
                result.extend(chunk)
            if bytes(result) != original:
                raise RuntimeError('GO_TO_PYTHON_MISMATCH')
        python_resource = root / 'python-resource'
        with python_resource.open('wb') as handle:
            writer = ResourceWriter(handle, key, 'm0-cross-process', len(original))
            writer.write(original)
            writer.close()
        verified = subprocess.run([str(binary), '--mode', 'verify', '--input', str(python_resource)],
                                  input=secret, check=True, capture_output=True)
        if verified.stdout != original:
            raise RuntimeError('PYTHON_TO_GO_MISMATCH')
        # Use the complete existing synthetic DOCX fixture. Read ZIP entries
        # directly through the authenticated reader, append ZIP through writer.
        import sys
        sys.path.insert(0, str(ROOT / 'python/tests'))
        from test_docx_package import fixture
        source_encoded = io.BytesIO()
        writer = ResourceWriter(source_encoded, key, 'docx-source', 1024**2)
        writer.write(fixture())
        writer.close()
        source_reader = ResourceReader(io.BytesIO(source_encoded.getvalue()), key, 'docx-source')
        destination = io.BytesIO()
        candidate_writer = ResourceWriter(destination, key, 'docx-candidate', 1024**2)
        rewrite_text_subset(source_reader, candidate_writer,
                            lambda part, paragraph: [Edit(3, 5, '[人名]')])
        candidate_writer.close()
        candidate_reader = ResourceReader(io.BytesIO(destination.getvalue()), key, 'docx-candidate')
        candidate_reader.verify()
        archive = Archive(candidate_reader)
        try:
            paragraphs = []
            with archive.open('word/document.xml') as part:
                scan(part, paragraphs.append)
            if paragraphs[0].text != '联系人[人名]结束':
                raise RuntimeError('ENCRYPTED_DOCX_MISMATCH')
        finally:
            archive.close()
        checks = {'go_to_python': True, 'python_to_go': True, 'multi_segment_bytes': len(original),
                  'docx_direct_encrypted_read_write': True}
    report = {'probe': 'm0-resource-process-interop', 'status': 'passed', 'checks': checks,
              'limitations': ['generated fixtures only', 'test paths, not production inherited-handle capability transport',
                              'not OS-handle revocation, sandbox or key-store evidence',
                              'not app pipeline or format admission']}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps(report, ensure_ascii=False))


if __name__ == '__main__':
    main()

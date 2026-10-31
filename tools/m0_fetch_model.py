#!/usr/bin/env python3
"""Development-only M0 resource preparation; never used by document processing.

Production download must use Go netgate and the release-signed resource catalog.
This script fetches only the fixed public model candidate recorded in the repo.
It sends no document, filename, local path or credential to the remote service.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'resources/models/astrlink-guard-int8.json'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--destination', type=Path, required=True)
    args = parser.parse_args()
    manifest = json.loads(MANIFEST.read_text())
    revision = manifest['revision']
    if not re.fullmatch('[0-9a-f]{40}', revision):
        raise SystemExit('M0_MODEL_REVISION_INVALID')
    destination = args.destination.resolve()
    destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name, expected in manifest['files'].items():
        if not re.fullmatch('[a-zA-Z0-9_.-]+', name) or not 0 < expected['size'] <= 50 * 1024**2:
            raise SystemExit('M0_MODEL_MANIFEST_INVALID')
        final = destination / name
        if final.is_symlink():
            raise SystemExit('M0_MODEL_DESTINATION_INVALID')
        if final.exists() and final.stat().st_size == expected['size']:
            with final.open('rb') as handle:
                if hashlib.file_digest(handle, 'sha256').hexdigest() == expected['sha256']:
                    print(f'{name}: verified existing file')
                    continue
        url = f'https://huggingface.co/QuantumNous/astrlink-guard/resolve/{revision}/int8/{name}'
        descriptor, temporary = tempfile.mkstemp(prefix='.m0-model-', dir=destination)
        temporary = Path(temporary)
        try:
            digest, size = hashlib.sha256(), 0
            with os.fdopen(descriptor, 'wb') as target:
                try:
                    response = urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'tacet-m0-resource-probe'}), timeout=60)
                    with response:
                        while chunk := response.read(64 * 1024):
                            size += len(chunk)
                            if size > expected['size']:
                                raise SystemExit('M0_MODEL_SIZE_MISMATCH')
                            digest.update(chunk)
                            target.write(chunk)
                except urllib.error.URLError:
                    raise SystemExit('M0_MODEL_FETCH_FAILED') from None
                target.flush()
                os.fsync(target.fileno())
            if size != expected['size'] or digest.hexdigest() != expected['sha256']:
                raise SystemExit('M0_MODEL_INTEGRITY_MISMATCH')
            temporary.replace(final)
            print(f'{name}: verified {size} bytes')
        finally:
            temporary.unlink(missing_ok=True)
    print('M0 fixed model files verified; not a release-signed installation')


if __name__ == '__main__':
    main()

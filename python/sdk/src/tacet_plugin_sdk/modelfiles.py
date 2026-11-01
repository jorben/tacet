"""Verify a model's files against its integrity manifest before importing inference code.

Shared by the built-in detection plugins.

Production trust comes from the application's signed catalog. This verifier
checks catalog structure and file content; it does not itself verify signatures.
"""
from __future__ import annotations

import hashlib
import re
from pathlib import Path


# Largest single file a manifest may list; the same bound as the application's catalog (internal/models).
MAX_FILE_SIZE = 8 * 1024**3


class IntegrityError(ValueError):
    pass


def verify(directory: Path, manifest: dict) -> None:
    if not re.fullmatch(r'[0-9a-f]{40}', manifest.get('revision', '')):
        raise IntegrityError('MODEL_REVISION_INVALID')
    directory = Path(directory)
    if directory.is_symlink() or not directory.is_dir():
        raise IntegrityError('MODEL_DIRECTORY_INVALID')
    files = manifest.get('files')
    if not isinstance(files, dict) or not files or len(files) > 128:
        raise IntegrityError('MODEL_MANIFEST_INVALID')
    for name, expected in files.items():
        if not re.fullmatch(r'[A-Za-z0-9_.-]+', name) or name in {'.', '..'}:
            raise IntegrityError('MODEL_MANIFEST_INVALID')
        if (not isinstance(expected, dict) or type(expected.get('size')) is not int
                or not 0 < expected["size"] <= MAX_FILE_SIZE
                or not re.fullmatch(r'[0-9a-f]{64}', expected.get('sha256', ''))):
            raise IntegrityError('MODEL_MANIFEST_INVALID')
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise IntegrityError('MODEL_FILE_MISSING')
        try:
            with path.open('rb') as source:
                size = 0
                digest = hashlib.sha256()
                while chunk := source.read(64 * 1024):
                    size += len(chunk)
                    if size > expected['size']:
                        raise IntegrityError('MODEL_SIZE_MISMATCH')
                    digest.update(chunk)
        except OSError:
            raise IntegrityError('MODEL_FILE_UNAVAILABLE') from None
        if size != expected['size']:
            raise IntegrityError('MODEL_SIZE_MISMATCH')
        if digest.hexdigest() != expected['sha256']:
            raise IntegrityError('MODEL_DIGEST_MISMATCH')

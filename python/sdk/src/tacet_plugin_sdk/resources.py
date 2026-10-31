"""Version-1 segmented encrypted resources, shared with Go internal/store.

Use inherited/host-proxied streams, not filesystem paths. Each candidate gets a
fresh salt/key; writes append ciphertext only. Readers authenticate the final
record on open and every accessed record; verify() explicitly checks all data.
This codec does not provide task authorization or revoke OS handles itself.
"""
from __future__ import annotations

import hashlib
import io
import os
import struct
from contextlib import contextmanager
from typing import BinaryIO

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

SEGMENT_BYTES = 64 * 1024
HEADER_BYTES = 74
RECORD_BYTES = SEGMENT_BYTES + 16
MAGIC = b'TACETRES'
VERSION = 1


class ResourceError(ValueError):
    pass


def _cipher(key, salt):
    if len(key) != 32:
        raise ResourceError('RESOURCE_INVALID')
    derived = HKDF(algorithm=hashes.SHA256(), length=32, salt=salt,
                   info=b'tacet/resource/v1').derive(key)
    return AESGCM(derived)


def _nonce(sequence, final):
    return bytes([int(final), 0, 0, 0]) + struct.pack('>Q', sequence)


def _aad(header, sequence, final):
    return header + struct.pack('>Q', sequence) + bytes([int(final)])


def _write(output, data):
    view = memoryview(data)
    while view:
        count = output.write(view)
        if count is None or not 0 < count <= len(view):
            raise ResourceError('RESOURCE_WRITE_FAILED')
        view = view[count:]


def _read(source, size):
    chunks = []
    while size:
        chunk = source.read(size)
        if not chunk:
            raise ResourceError('RESOURCE_INVALID')
        chunks.append(chunk)
        size -= len(chunk)
    return b''.join(chunks)


class ResourceWriter:
    def __init__(self, output: BinaryIO, key: bytes, resource_id: str, limit: int):
        if not resource_id or len(resource_id.encode()) > 1024 or limit <= 0:
            raise ResourceError('RESOURCE_INVALID')
        self.header = MAGIC + struct.pack('>H', VERSION) + os.urandom(32) + hashlib.sha256(resource_id.encode()).digest()
        self._cipher = _cipher(key, self.header[10:42])
        self._output = output
        self._buffer = bytearray()
        self._sequence = 0
        self._written = 0
        self._limit = limit
        self._closed = False
        _write(output, self.header)

    def _segment(self, final):
        encrypted = self._cipher.encrypt(_nonce(self._sequence, final), bytes(self._buffer),
                                         _aad(self.header, self._sequence, final))
        try:
            _write(self._output, encrypted)
        except Exception:
            self._closed = True
            raise
        finally:
            self._buffer[:] = b'\0' * len(self._buffer)
            self._buffer.clear()
        self._sequence += 1

    def write(self, data):
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        if len(data) > self._limit - self._written:
            self._closed = True
            self._buffer[:] = b'\0' * len(self._buffer)
            self._buffer.clear()
            raise ResourceError('RESOURCE_QUOTA')
        view = memoryview(data)
        size = len(view)
        while view:
            if len(self._buffer) == SEGMENT_BYTES:
                self._segment(False)
            used = min(len(view), SEGMENT_BYTES - len(self._buffer))
            self._buffer.extend(view[:used])
            view = view[used:]
            self._written += used
        return size

    def flush(self):
        # Flushing buffered plaintext would make it impossible to mark the
        # actual final record without seeking/reusing a nonce. Only Close seals.
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')

    def close(self):
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        try:
            self._segment(True)
        finally:
            self._closed = True
            self._cipher = None


@contextmanager
def inherited_reader(reference: dict):
    """Open only a host-issued inherited descriptor; never interpret a path.

    Schema and attempt admission are required before this boundary. Platforms
    without CRT-compatible inherited descriptors need the host proxy adapter.
    """
    descriptor = reference.get('handle')
    if type(descriptor) is not int or descriptor < 3 or reference.get('mode') != 'read':
        raise ResourceError('RESOURCE_CAPABILITY_INVALID')
    owned = None
    try:
        owned = os.fdopen(os.dup(descriptor), 'rb')
        encryption = reference.get('encryption')
        if encryption:
            key = bytes.fromhex(encryption['key_hex'])
            source = ResourceReader(owned, key, encryption['resource_id'])
        else:
            source = owned
        try:
            yield source
        finally:
            if source is not owned:
                source.close()
    except (OSError, KeyError, ValueError):
        raise ResourceError('RESOURCE_CAPABILITY_INVALID') from None
    finally:
        if owned is not None:
            owned.close()


@contextmanager
def inherited_writer(reference: dict, limit: int):
    """Open a host-issued inherited append-only descriptor as an encrypted writer.

    The host opens the descriptor with O_APPEND; the segment writer only appends
    ciphertext. A plaintext (unencrypted) write capability is refused.
    """
    descriptor = reference.get('handle')
    encryption = reference.get('encryption')
    if type(descriptor) is not int or descriptor < 3 or reference.get('mode') != 'write' or not encryption:
        raise ResourceError('RESOURCE_CAPABILITY_INVALID')
    owned = None
    try:
        owned = os.fdopen(os.dup(descriptor), 'ab')
        writer = ResourceWriter(owned, bytes.fromhex(encryption['key_hex']), encryption['resource_id'], limit)
        yield writer
        writer.close()
    except (OSError, KeyError, ValueError):
        raise ResourceError('RESOURCE_CAPABILITY_INVALID') from None
    finally:
        if owned is not None:
            owned.close()


class ResourceReader:
    def __init__(self, source: BinaryIO, key: bytes, resource_id: str):
        self._source = source
        self._closed = False
        self._position = 0
        source.seek(0, io.SEEK_END)
        size = source.tell()
        if size < HEADER_BYTES + 16 or not resource_id or len(resource_id.encode()) > 1024:
            raise ResourceError('RESOURCE_INVALID')
        source.seek(0)
        self.header = _read(source, HEADER_BYTES)
        if self.header[:8] != MAGIC:
            raise ResourceError('RESOURCE_INVALID')
        if struct.unpack('>H', self.header[8:10])[0] != VERSION:
            raise ResourceError('RESOURCE_VERSION_UNSUPPORTED')
        if self.header[42:] != hashlib.sha256(resource_id.encode()).digest():
            raise ResourceError('RESOURCE_INVALID')
        self._cipher = _cipher(key, self.header[10:42])
        body = size - HEADER_BYTES
        self._segments = (body - 1) // RECORD_BYTES + 1
        self._last = body - (self._segments - 1) * RECORD_BYTES
        if not 16 <= self._last <= RECORD_BYTES:
            raise ResourceError('RESOURCE_INVALID')
        self.size = (self._segments - 1) * SEGMENT_BYTES + self._last - 16
        self._segment(self._segments - 1)

    def _segment(self, sequence):
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        final = sequence == self._segments - 1
        count = self._last if final else RECORD_BYTES
        self._source.seek(HEADER_BYTES + sequence * RECORD_BYTES)
        encrypted = _read(self._source, count)
        try:
            return self._cipher.decrypt(_nonce(sequence, final), encrypted,
                                        _aad(self.header, sequence, final))
        except InvalidTag:
            raise ResourceError('RESOURCE_INVALID') from None

    def read(self, size=-1):
        # Enforce bounded reads at the SDK boundary. Parser requests for the
        # whole stream must use explicit chunking rather than materialize it.
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        if not 0 <= size <= 1024 * 1024:
            raise ResourceError('RESOURCE_READ_LIMIT')
        left = min(size, max(0, self.size - self._position))
        output = bytearray()
        while left:
            sequence, offset = divmod(self._position, SEGMENT_BYTES)
            segment = self._segment(sequence)
            used = min(left, len(segment) - offset)
            output.extend(segment[offset:offset+used])
            self._position += used
            left -= used
        return bytes(output)

    def readinto(self, buffer):
        data = self.read(len(buffer))
        buffer[:len(data)] = data
        return len(data)

    def seek(self, offset, whence=io.SEEK_SET):
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        if whence == io.SEEK_CUR:
            offset += self._position
        elif whence == io.SEEK_END:
            offset += self.size
        elif whence != io.SEEK_SET:
            raise ResourceError('RESOURCE_INVALID')
        if offset < 0:
            raise ResourceError('RESOURCE_INVALID')
        self._position = offset
        return offset

    @property
    def closed(self):
        # libraries such as olefile probe this on the file object they are given
        return self._closed

    def readable(self):
        return not self._closed

    def writable(self):
        return False

    def seekable(self):
        return not self._closed

    def tell(self):
        if self._closed:
            raise ResourceError('RESOURCE_CLOSED')
        return self._position

    def verify(self):
        for sequence in range(self._segments):
            self._segment(sequence)

    def close(self):
        self._closed = True
        self._cipher = None
        # Python immutable bytes and backend cipher memory cannot be reliably
        # zeroed. Drop references; process lifecycle provides the outer bound.
        self.header = b''

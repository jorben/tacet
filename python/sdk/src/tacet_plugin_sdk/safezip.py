"""Budgeted ZIP structural gate shared by OOXML format plugins.

Inspect central and local headers before allowing zipfile to interpret entries.
Inputs are seekable resource streams, never user paths. ZIP64 and multi-disk
containers currently fail explicitly; this is an M0 subset, not format admission.
No entry is extracted to the filesystem.
"""
from __future__ import annotations

import io
import struct
import zipfile
import zlib
from contextlib import contextmanager
from dataclasses import dataclass
from typing import BinaryIO, Iterator

_EOCD = struct.Struct("<4s4H2IH")
_CENTRAL = struct.Struct("<4s6H3I5H2I")
_LOCAL = struct.Struct("<4s5H3I2H")


class ArchiveError(ValueError):
    """A stable, non-sensitive rejection code; never include entry names."""


@dataclass(frozen=True)
class Limits:
    entries: int = 16_384
    directory_bytes: int = 16 * 1024 * 1024
    expanded_bytes: int = 8 * 1024**3
    entry_bytes: int = 1024**3
    ratio: int = 1000

    def __post_init__(self) -> None:
        if any(value <= 0 for value in vars(self).values()):
            raise ValueError("ARCHIVE_INVALID_LIMITS")


@dataclass(frozen=True)
class Entry:
    name: str
    size: int
    compressed: int
    crc: int
    method: int
    flags: int
    offset: int
    data_offset: int
    end: int


def _read(stream: BinaryIO, size: int) -> bytes:
    chunks = []
    remaining = size
    while remaining:
        chunk = stream.read(remaining)
        if not chunk:
            raise ArchiveError("ZIP_TRUNCATED")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def _name(raw: bytes, flags: int) -> str:
    try:
        name = raw.decode("utf-8" if flags & 0x800 else "cp437")
    except UnicodeDecodeError:
        raise ArchiveError("ZIP_INVALID_NAME") from None
    if (not name or "\0" in name or "\\" in name or name.startswith("/")
            or ":" in name or any(p in {".", ".."} for p in name.split("/"))):
        raise ArchiveError("ZIP_UNSAFE_NAME")
    return name


def _extra(raw: bytes) -> None:
    cursor = 0
    while cursor < len(raw):
        if cursor + 4 > len(raw):
            raise ArchiveError("ZIP_INVALID_EXTRA")
        tag, size = struct.unpack_from("<HH", raw, cursor)
        cursor += 4
        if cursor + size > len(raw):
            raise ArchiveError("ZIP_INVALID_EXTRA")
        if tag == 1:
            raise ArchiveError("ZIP64_UNSUPPORTED")
        # Unicode alternate paths produce two interpretations of one identity.
        if tag == 0x7075:
            raise ArchiveError("ZIP_ALTERNATE_NAME_UNSUPPORTED")
        cursor += size


def inspect(stream: BinaryIO, limits: Limits = Limits()) -> tuple[Entry, ...]:
    """Return every central-directory entry after checking the whole structure.

    Entry count, metadata, declared expansion and compression ratio are bounded
    before constructing ZipFile. Local ranges (including data descriptors) may
    not overlap or point into the central directory. No preamble or trailing
    bytes are accepted, because OOXML has no need for executable ZIP wrappers.
    """
    stream.seek(0, io.SEEK_END)
    length = stream.tell()
    if length < _EOCD.size:
        raise ArchiveError("ZIP_TRUNCATED")
    stream.seek(max(0, length - 65_557))
    tail = stream.read(65_557)
    index = tail.rfind(b"PK\x05\x06")
    if index < 0 or index + _EOCD.size > len(tail):
        raise ArchiveError("ZIP_MISSING_DIRECTORY")
    _, disk, cd_disk, disk_count, count, cd_size, cd_start, comment = _EOCD.unpack_from(tail, index)
    eocd_start = length - len(tail) + index
    if index + _EOCD.size + comment != len(tail):
        raise ArchiveError("ZIP_TRAILING_DATA")
    if disk or cd_disk or disk_count != count:
        raise ArchiveError("ZIP_MULTIDISK_UNSUPPORTED")
    if count == 0xFFFF or cd_start == 0xFFFFFFFF or cd_size == 0xFFFFFFFF:
        raise ArchiveError("ZIP64_UNSUPPORTED")
    if count > limits.entries or cd_size > limits.directory_bytes:
        raise ArchiveError("ZIP_DIRECTORY_LIMIT")
    if cd_start + cd_size != eocd_start:
        raise ArchiveError("ZIP_DIRECTORY_MISMATCH")
    stream.seek(cd_start)
    records = []
    names: set[str] = set()
    expanded = 0
    for _ in range(count):
        if stream.tell() + _CENTRAL.size > eocd_start:
            raise ArchiveError("ZIP_DIRECTORY_MISMATCH")
        fields = _CENTRAL.unpack(_read(stream, _CENTRAL.size))
        (signature, made, version, flags, method, mtime, mdate, crc, compressed,
         size, name_len, extra_len, comment_len, disk_no, internal, external, offset) = fields
        if signature != b"PK\x01\x02":
            raise ArchiveError("ZIP_DIRECTORY_MISMATCH")
        if stream.tell() + name_len + extra_len + comment_len > eocd_start:
            raise ArchiveError("ZIP_DIRECTORY_MISMATCH")
        raw_name = _read(stream, name_len)
        name = _name(raw_name, flags)
        _extra(_read(stream, extra_len))
        _read(stream, comment_len)
        if name in names:
            raise ArchiveError("ZIP_DUPLICATE_ENTRY")
        names.add(name)
        if disk_no:
            raise ArchiveError("ZIP_MULTIDISK_UNSUPPORTED")
        if max(size, compressed, offset) == 0xFFFFFFFF or version >= 45:
            raise ArchiveError("ZIP64_UNSUPPORTED")
        if flags & ~0x80E:  # deflate options, descriptor, UTF-8 only
            raise ArchiveError("ZIP_FLAGS_UNSUPPORTED")
        if method not in {zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED}:
            raise ArchiveError("ZIP_COMPRESSION_UNSUPPORTED")
        # Reject Unix symlinks even though we never extract to paths.
        if external >> 16 & 0o170000 == 0o120000:
            raise ArchiveError("ZIP_SYMLINK_UNSUPPORTED")
        expanded += size
        if size > limits.entry_bytes or expanded > limits.expanded_bytes:
            raise ArchiveError("ZIP_EXPANSION_LIMIT")
        if size > max(1, compressed) * limits.ratio:
            raise ArchiveError("ZIP_RATIO_LIMIT")
        records.append((raw_name, name, flags, method, crc, compressed, size, offset))
    if stream.tell() != eocd_start:
        raise ArchiveError("ZIP_DIRECTORY_MISMATCH")
    entries = []
    for raw_name, name, flags, method, crc, compressed, size, offset in records:
        if offset + _LOCAL.size > cd_start:
            raise ArchiveError("ZIP_LOCAL_MISMATCH")
        stream.seek(offset)
        (signature, version, local_flags, local_method, mtime, mdate,
         local_crc, local_compressed, local_size, name_len, extra_len) = _LOCAL.unpack(_read(stream, _LOCAL.size))
        if signature != b"PK\x03\x04" or local_flags != flags or local_method != method:
            raise ArchiveError("ZIP_LOCAL_MISMATCH")
        if stream.tell() + name_len + extra_len > cd_start:
            raise ArchiveError("ZIP_LOCAL_MISMATCH")
        if _read(stream, name_len) != raw_name:
            raise ArchiveError("ZIP_LOCAL_MISMATCH")
        _extra(_read(stream, extra_len))
        if flags & 8:
            if (local_crc, local_compressed, local_size) not in {(0, 0, 0), (crc, compressed, size)}:
                raise ArchiveError("ZIP_LOCAL_MISMATCH")
        elif (local_crc, local_compressed, local_size) != (crc, compressed, size):
            raise ArchiveError("ZIP_LOCAL_MISMATCH")
        data_offset = stream.tell()
        end = data_offset + compressed
        if end > cd_start:
            raise ArchiveError("ZIP_ENTRY_OVERLAP")
        if flags & 8:
            stream.seek(end)
            first = _read(stream, 4)
            if first == b"PK\x07\x08":
                descriptor = _read(stream, 12)
                end += 16
            else:
                descriptor = first + _read(stream, 8)
                end += 12
            if struct.unpack("<III", descriptor) != (crc, compressed, size):
                raise ArchiveError("ZIP_DESCRIPTOR_MISMATCH")
        entries.append(Entry(name, size, compressed, crc, method, flags, offset, data_offset, end))
    cursor = 0
    for entry in sorted(entries, key=lambda e: e.offset):
        if entry.offset != cursor or entry.end > cd_start:
            raise ArchiveError("ZIP_ENTRY_OVERLAP" if entry.offset < cursor else "ZIP_UNREGISTERED_DATA")
        cursor = entry.end
    if cursor != cd_start:
        raise ArchiveError("ZIP_UNREGISTERED_DATA")
    return tuple(entries)


class Archive:
    """Validated bounded reader. The caller owns the input resource stream."""

    def __init__(self, stream: BinaryIO, limits: Limits = Limits()):
        self.entries = inspect(stream, limits)
        self._limits = limits
        self._read_bytes = 0
        self._entries = {entry.name: entry for entry in self.entries}
        try:
            self._zip = zipfile.ZipFile(stream, "r")
        except (zipfile.BadZipFile, ValueError):
            raise ArchiveError("ZIP_INVALID") from None

    @contextmanager
    def open(self, name: str) -> Iterator[BinaryIO]:
        if name not in self._entries:
            raise ArchiveError("ZIP_ENTRY_MISSING")
        try:
            with self._zip.open(name, "r") as raw:
                yield _Reader(raw, self)
        except (zipfile.BadZipFile, RuntimeError, EOFError, zlib.error):
            raise ArchiveError("ZIP_ENTRY_CORRUPT") from None

    def close(self) -> None:
        self._zip.close()


class _Reader:
    def __init__(self, raw: BinaryIO, owner: Archive):
        self._raw = raw
        self._owner = owner

    def read(self, size: int = -1) -> bytes:
        if size < 0 or size > 1024 * 1024:
            raise ArchiveError("ZIP_READ_LIMIT")
        chunk = self._raw.read(size)
        self._owner._read_bytes += len(chunk)
        if self._owner._read_bytes > self._owner._limits.expanded_bytes:
            raise ArchiveError("ZIP_EXPANSION_LIMIT")
        return chunk

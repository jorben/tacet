"""Message framing of the plugin protocol (architecture 4.2).

A frame is a 4-byte big-endian length followed by that many bytes of UTF-8
JSON. The length is checked against the message limit before the body is
read, so an oversized length never allocates a buffer.
"""

import json
import struct
from typing import Any, BinaryIO

MAX_MESSAGE_BYTES = 4 * 1024 * 1024
_HEADER = struct.Struct(">I")


class FrameError(Exception):
    """The peer sent a frame that breaks the protocol."""


def encode(message: Any) -> bytes:
    """Encode one message as a frame."""
    body = json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(body) > MAX_MESSAGE_BYTES:
        raise FrameError(f"message of {len(body)} bytes exceeds {MAX_MESSAGE_BYTES}")
    return _HEADER.pack(len(body)) + body


def read(stream: BinaryIO) -> Any | None:
    """Read one message, or return None at a clean end of stream."""
    header = _read_exact(stream, _HEADER.size, eof_ok=True)
    if header is None:
        return None
    (size,) = _HEADER.unpack(header)
    if size > MAX_MESSAGE_BYTES:
        raise FrameError(f"frame of {size} bytes exceeds {MAX_MESSAGE_BYTES}")
    body = _read_exact(stream, size, eof_ok=False)
    try:
        return json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise FrameError("frame body is not UTF-8 JSON") from exc


def _read_exact(stream: BinaryIO, size: int, eof_ok: bool) -> bytes | None:
    chunks = []
    left = size
    while left:
        chunk = stream.read(left)
        if not chunk:
            if eof_ok and left == size:
                return None
            raise FrameError("stream ended inside a frame")
        chunks.append(chunk)
        left -= len(chunk)
    return b"".join(chunks)

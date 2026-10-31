"""Small helpers around the sink a plugin writes its candidate to."""
from __future__ import annotations


class _Counting:
    def __init__(self, sink):
        self._sink = sink
        self.bytes = 0

    def write(self, data):
        self.bytes += len(data)
        return self._sink.write(data)

    def flush(self):
        flush = getattr(self._sink, 'flush', None)
        if flush:
            flush()


def counting(sink):
    """A write-through that counts the bytes the candidate ended up with."""
    return _Counting(sink)

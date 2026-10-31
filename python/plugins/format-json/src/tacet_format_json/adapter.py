"""JSON and JSON Lines as the unit-based protocol sees them.

Three parts, all read: ``values`` (string values, the only part this plugin edits), ``keys`` (object
keys) and ``numbers`` (number lexemes of at least seven digits, where a phone or identification number
stored as a number lives). Renaming a key or turning a number into a string changes the document's
type structure, which needs the plan's explicit authorization, so operations on ``keys`` and ``numbers``
are refused with a reason code: a finding there fails the export instead of being passed over.

Units are numbered by structure so they do not move when a string changes length: a string value
is ``string index * 64 + piece`` (pieces of 16384 code points, at most 64), a key or a number is its running
count in its part.
"""
from __future__ import annotations

from tacet_plugin_sdk.byteedit import Edit
from tacet_plugin_sdk.unitkit import Adapter, Unit

from .scanner import KEY, NUMBER, STRING_BEGIN, STRING_CHUNK, STRING_END, JsonError, Limits, scan, string_content

PIECE = 16384
PIECES = 64
MIN_NUMBER_DIGITS = 7


def _drain(source, jsonl) -> bool:
    source.seek(0)
    for _ in scan(source, Limits(), jsonl=jsonl):
        pass
    return True


def _complete(source, jsonl) -> bool:
    source.seek(0)
    try:
        return any(True for _ in scan(source, Limits(), jsonl=jsonl)) and _drain(source, jsonl)
    except JsonError:
        return False
    finally:
        source.seek(0)


class JsonAdapter(Adapter):
    format = 'json'
    content_type = 'application/json'
    parts = (('values', 'text'), ('keys', 'text'), ('numbers', 'text'))
    editable = frozenset({'values'})

    def __init__(self, jsonl):
        self.jsonl = jsonl
        self.format = 'jsonl' if jsonl else 'json'
        if jsonl:
            self.content_type = 'application/x-ndjson'

    def units(self, source):
        source.seek(0)
        strings = keys = numbers = 0
        text, offs = [], []
        for event in scan(source, Limits(), jsonl=self.jsonl):
            if event.kind == STRING_BEGIN:
                strings, text, offs = strings + 1, [], []
            elif event.kind == STRING_CHUNK:
                # the scanner cuts chunks wherever its read buffer ends, which moves when bytes before
                # the string change length; the string is therefore cut again here at fixed sizes
                if sum(map(len, text)) + len(event.text) > PIECE * PIECES:
                    raise JsonError('string_too_long', event.start)
                text.append(event.text)
                offs.extend(event.offsets[:-1])
                end = event.offsets[-1]
            elif event.kind == STRING_END:
                whole = ''.join(text)
                offs.append(end if text else event.start)
                for k in range(0, (len(whole) + PIECE - 1) // PIECE):
                    yield Unit('values', strings * PIECES + k, whole[k * PIECE:(k + 1) * PIECE], offs[k * PIECE:min((k + 1) * PIECE, len(whole)) + 1])
                if not whole:
                    yield Unit('values', strings * PIECES, '', [offs[-1], offs[-1]])
            elif event.kind == KEY:
                keys += 1
                yield Unit('keys', keys, event.text)
            elif event.kind == NUMBER and sum(c.isdigit() for c in event.text) >= MIN_NUMBER_DIGITS:
                numbers += 1
                yield Unit('numbers', numbers, event.text)

    def edits(self, unit, ops):
        return [Edit(unit.offs[op.start], unit.offs[op.end], string_content(op.replacement)) for op in ops]


def open_adapter(source):
    for jsonl in (False, True):
        if _complete(source, jsonl):
            return JsonAdapter(jsonl)
    return None

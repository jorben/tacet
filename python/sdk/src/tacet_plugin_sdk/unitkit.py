"""One protocol implementation for formats whose text is a sequence of editable units.

TXT, CSV and JSON all look the same to the host: a part, a unit ordinal inside it, the unit's text, and
byte spans that let the plugin replace a code point range of that text in place. An ``Adapter`` supplies
those units and turns a range replacement into a byte ``Edit``; this module does everything the protocol
asks around it (probe, extract, write, validate), so the strictness is written once:

* extraction reports every part and a completion marker with counts;
* write reports every operation as executed or not executed with a reason code, and every part's fate;
* validation re-opens the candidate on its own, reads it again and judges each operation on the text it finds.

Units are addressed ``(part, ordinal)`` and an ordinal must not move when other units are edited, so
adapters number units by structure (line, cell, string), not by running offset.
"""
from __future__ import annotations

import re
from collections import defaultdict
from dataclasses import dataclass
from typing import Callable, Iterator, Sequence

from . import byteedit
from .byteedit import Edit, FormatError
from .inherited import counting
from .plan import Op, read_plan
from .resources import inherited_reader, inherited_writer
from .runtime import TaskError
from .schema import Schemas
from .textjudge import judge

MAX_UNIT_CHARS = 1 << 20
_CODE = re.compile(r'^[A-Z][A-Z0-9_]*$')


@dataclass
class Unit:
    part: str
    ordinal: int
    text: str
    offs: Sequence[int] | None = None  # byte offset of every code point plus the end, when the format has them
    ref: object = None                 # whatever the adapter needs to build an edit (a cell, a chunk)


class Adapter:
    format = ''
    content_type = ''
    parts: tuple[tuple[str, str], ...] = (('content', 'text'),)  # (part id, class)
    editable: frozenset = frozenset({'content'})
    removable: frozenset = frozenset()   # parts a ``part`` operation may remove
    virtual: frozenset = frozenset()     # unit part ids that are not package parts (read-only views)

    def enumerate(self, source) -> list[tuple[str, str, str, str]]:
        """(part id, content type, class, state) of every part of the file as it is now.

        Validation calls this on the candidate as well, so it must read the file, not remember it.
        """
        return [(name, self.content_type, cls, 'inspected') for name, cls in self.parts]

    def units(self, source) -> Iterator[Unit]:
        raise NotImplementedError

    def edits(self, unit: Unit, ops: list[Op]) -> list:
        """Edits for non-overlapping, in-range operations of one unit, in file order."""
        raise NotImplementedError

    def also_rewritten(self, touched: set) -> frozenset:
        """Parts the write changes besides the ones whose units were edited (e.g. a shared string table)."""
        return frozenset()

    def apply(self, source, sink, edits: list, removals: list[str]) -> None:
        """Write the file with the edits applied and the removals left out."""
        if removals:
            raise FormatError('part_removal_unsupported', 0)
        byteedit.rewrite(source, sink, edits)


Opener = Callable[[object], 'Adapter | None']


def _guard(iterator):
    """Turn a scanner's stable format errors into the protocol's input-corrupt failure."""
    try:
        yield from iterator
    except FormatError:
        raise TaskError('INPUT_CORRUPT') from None


def _enumerate(adapter, source):
    try:
        return adapter.enumerate(source)
    except FormatError:
        raise TaskError('INPUT_CORRUPT') from None
    finally:
        source.seek(0)


def _size(source) -> int:
    source.seek(0, 2)
    n = source.tell()
    source.seek(0)
    return n


def probe(source, opener: Opener) -> dict:
    size = _size(source)
    adapter = opener(source)
    source.seek(0)
    if adapter is None:
        return {'kind': 'probe_result', 'format': 'unknown', 'encryption': 'none', 'pages': 0, 'images': 0, 'embedded': 0, 'bytes': size}
    return {'kind': 'probe_result', 'format': adapter.format, 'encryption': 'none', 'pages': 0, 'images': 0, 'embedded': 0, 'bytes': size}


def extract(adapter: Adapter, source, task, schemas) -> None:
    def emit(item):
        schemas.validate('format.schema.json', 'ExtractItem', item)
        task.emit(item)

    n = 0
    for unit in _guard(adapter.units(source)):
        task.check()
        if not unit.text:
            continue
        if len(unit.text) > MAX_UNIT_CHARS:
            raise TaskError('RESOURCE_LIMIT')
        n += 1
        emit({'kind': 'text_unit', 'unit_id': f'u{n}', 'part': unit.part, 'ordinal': unit.ordinal, 'text': unit.text, 'visibility': 'visible',
              'source_map': {'kind': 'paragraph_ordinal', 'part': unit.part, 'ordinal': unit.ordinal}})
    parts = _enumerate(adapter, source)
    for name, _type, cls, state in parts:
        item = {'kind': 'part_state', 'part': name, 'class': cls, 'state': state}
        if state != 'inspected':
            item['reason'] = 'PART_NOT_INSPECTED'
        emit(item)
    emit({'kind': 'extract_complete', 'complete': True, 'units': n, 'parts': len(parts)})


def _apply(text: str, ops: list[Op]) -> str:
    for op in sorted(ops, key=lambda o: o.start, reverse=True):
        text = text[:op.start] + op.replacement + text[op.end:]
    return text


def apply_ops(text: str, ops: list[Op]) -> str:
    """The unit text after its operations (for adapters that replace a whole value)."""
    return _apply(text, ops)


def write(adapter: Adapter, source, ops: list[Op], sink, task, schemas) -> None:
    def emit(item):
        schemas.validate('format.schema.json', 'WriteItem', item)
        task.emit(item)

    parts = _enumerate(adapter, source)
    names = {p[0] for p in parts}
    results: dict[str, tuple[str, str | None]] = {}
    by_unit: dict[tuple[str, int], list[Op]] = defaultdict(list)
    removals: dict[str, list[str]] = defaultdict(list)
    for op in ops:
        if op.kind == 'part':
            if op.part in adapter.removable and op.part in names:
                removals[op.part].append(op.id)
            else:
                results[op.id] = ('not_executed', 'PART_OPERATION_UNSUPPORTED' if op.part in names else 'PART_UNKNOWN')
        elif op.part not in names and op.part not in adapter.virtual:
            results[op.id] = ('not_executed', 'PART_NOT_TEXT')
        elif op.part not in adapter.editable:
            results[op.id] = ('not_executed', 'PART_OPERATION_UNSUPPORTED')
        else:
            by_unit[(op.part, op.ordinal)].append(op)
    edits: list = []
    touched: set[str] = set()
    for unit in _guard(adapter.units(source)):
        task.check()
        here = by_unit.get((unit.part, unit.ordinal))
        if not here:
            continue
        good, last = [], 0
        for op in sorted(here, key=lambda o: (o.start, o.end)):
            if op.start >= op.end or op.end > len(unit.text) or op.start < last:
                results[op.id] = ('not_executed', 'RANGE_INVALID')
                continue
            good.append(op)
            last = op.end
        if not good:
            continue
        try:
            edits.extend(adapter.edits(unit, good))
        except FormatError as error:
            code = error.code.upper()
            for op in good:
                results[op.id] = ('not_executed', code if _CODE.match(code) else 'EDIT_FAILED')
            continue
        touched.add(unit.part)
        for op in good:
            results[op.id] = ('executed', None)
    for ids in removals.values():
        for i in ids:
            results[i] = ('executed', None)
    out = counting(sink)
    failure = None
    source.seek(0)
    try:
        adapter.apply(source, out, edits, sorted(removals))
    except FormatError as error:
        failure = error.code.upper() if _CODE.match(error.code.upper()) else 'REWRITE_FAILED'
    if failure:
        for op in ops:
            results[op.id] = ('not_executed', failure[:64])
        touched, removals = set(), {}
    for op in ops:
        status, reason = results.get(op.id, ('not_executed', 'UNIT_NOT_FOUND'))
        item = {'kind': 'op_result', 'op': op.id, 'status': status}
        if reason:
            item['reason'] = reason
        emit(item)
    also = adapter.also_rewritten(touched) if touched else frozenset()
    for name, *_ in parts:
        fate = 'removed' if name in removals else ('redacted' if name in touched else 'kept')
        if fate == 'kept' and name in also:
            fate = 'redacted'
        emit({'kind': 'part_fate', 'part': name, 'fate': fate})
    emit({'kind': 'write_complete', 'complete': True, 'operations': len(ops), 'parts': len(parts), 'bytes': max(out.bytes, 1)})


def validate(adapter: Adapter, source, opener: Opener, cand, ops: list[Op], task, schemas) -> None:
    def emit(item):
        schemas.validate('format.schema.json', 'ValidateItem', item)
        task.emit(item)

    in_parts = _enumerate(adapter, source)
    for name, ctype, *_ in in_parts:
        emit({'kind': 'input_part', 'part': name, 'content_type': ctype})
    wanted = {(op.part, op.ordinal) for op in ops if op.kind == 'text'}
    before = {}
    for unit in _guard(adapter.units(source)):
        task.check()
        if (unit.part, unit.ordinal) in wanted:
            before[(unit.part, unit.ordinal)] = unit.text
    after, reopened, out_parts = {}, True, []
    try:
        reopen = opener(cand)
        if reopen is None or reopen.format != adapter.format:
            reopened = False
        else:
            out_parts = reopen.enumerate(cand)
            for unit in reopen.units(cand):
                task.check()
                if (unit.part, unit.ordinal) in wanted:
                    after[(unit.part, unit.ordinal)] = unit.text
    except FormatError:
        reopened = False
    if not reopened:
        emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(in_parts), 'output_parts': 0, 'operations': 0, 'reopened': False})
        return
    input_names = {p[0] for p in in_parts}
    present = set()
    for name, ctype, *_ in out_parts:
        present.add(name)
        item = {'kind': 'output_part', 'part': name, 'content_type': ctype, 'references': []}
        if name in input_names:
            item['source'] = name
        emit(item)
    groups: dict[tuple[str, int], list[Op]] = defaultdict(list)
    verdict: dict[str, tuple[bool, bool]] = {}
    for op in ops:
        if op.kind == 'part':
            gone = op.part in input_names and op.part in adapter.removable and op.part not in present
            verdict[op.id] = (gone, op.part in present and op.part in adapter.removable)
        elif (op.part, op.ordinal) in before and (op.part, op.ordinal) in after:
            groups[(op.part, op.ordinal)].append(op)
        else:
            verdict[op.id] = (False, False)
    for key, group in groups.items():
        verdict.update(judge(before[key], after[key], group))
    for op in ops:
        executed, residual = verdict[op.id]
        unit = f'{op.part}#{op.ordinal}' if op.kind == 'text' else op.part
        emit({'kind': 'op_check', 'op': op.id, 'executed': executed, 'residual': residual, 'unit': unit})
    emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(in_parts), 'output_parts': len(out_parts), 'operations': len(ops), 'reopened': True})


def handlers(opener: Opener) -> dict:
    """The five protocol handlers of a unit-based format plugin."""
    def need(source):
        adapter = opener(source)
        source.seek(0)
        if adapter is None:
            raise TaskError('FORMAT_UNSUPPORTED')
        return adapter

    def schemas_for(*defs):
        s = Schemas()
        for d in defs:
            s.compile('format.schema.json', d)
        return s

    def probe_handler(task, params):
        s = schemas_for('ProbeParams', 'ProbeResult')
        s.validate('format.schema.json', 'ProbeParams', params)
        with inherited_reader(params['input']) as source:
            result = probe(source, opener)
        s.validate('format.schema.json', 'ProbeResult', result)
        task.emit(result)

    def extract_handler(task, params):
        s = schemas_for('ExtractParams', 'ExtractItem')
        s.validate('format.schema.json', 'ExtractParams', params)
        with inherited_reader(params['input']) as source:
            extract(need(source), source, task, s)

    def write_handler(task, params):
        s = schemas_for('WriteParams', 'WriteItem', 'PlanLine')
        s.validate('format.schema.json', 'WriteParams', params)
        with inherited_reader(params['plan']) as plan_source:
            ops = read_plan(plan_source, s)
        with inherited_reader(params['input']) as source, inherited_writer(params['candidate'], 8 << 30) as sink:
            write(need(source), source, ops, sink, task, s)

    def validate_handler(task, params):
        s = schemas_for('ValidateParams', 'ValidateItem', 'PlanLine')
        s.validate('format.schema.json', 'ValidateParams', params)
        with inherited_reader(params['plan']) as plan_source:
            ops = read_plan(plan_source, s)
        with inherited_reader(params['input']) as source, inherited_reader(params['candidate']) as cand:
            validate(need(source), source, opener, cand, ops, task, s)

    return {'format.probe': probe_handler, 'format.extract': extract_handler, 'format.write': write_handler, 'format.validate': validate_handler}

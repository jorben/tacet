"""``format.validate`` for the DOCX text subset.

Independent of the writer's own report: the candidate is re-opened and re-read,
every output part is enumerated with its content type and references, and each
planned operation is checked against what is actually in the candidate. The host
reconciles the part lists, re-detects on the candidate's text itself and decides;
this reports facts only. A candidate that cannot be re-opened is reported as
such, never as an empty success.
"""
from __future__ import annotations

import posixpath
from collections import defaultdict

from tacet_plugin_sdk.opc import PackageError
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.safezip import Archive, ArchiveError

from .package import inventory
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.textjudge import judge  # noqa: F401  (re-exported: the DOCX tests use it from here)
from .structure import inspect
from .xmlparts import XMLPartError, scan


def _rels_path(source):
    if not source:
        return '_rels/.rels'
    return posixpath.join(posixpath.dirname(source), '_rels', posixpath.basename(source) + '.rels')


def _paragraphs(archive, name, ordinals):
    found = {}

    def take(paragraph):
        if paragraph.ordinal in ordinals:
            found[paragraph.ordinal] = paragraph.text

    with archive.open(name) as stream:
        scan(stream, take)
    return found


def validate(inp, ops: list[Op], cand, task, schemas) -> None:
    def emit(item):
        schemas.validate('format.schema.json', 'ValidateItem', item)
        task.emit(item)

    try:
        src = Archive(inp)
    except ArchiveError:
        raise TaskError('INPUT_CORRUPT') from None
    try:
        before = inventory(src)
        for p in before.parts:
            emit({'kind': 'input_part', 'part': p.name, 'content_type': p.content_type or 'application/octet-stream'})
        try:
            out = Archive(cand)
            after = inventory(out)
        except (PackageError, ArchiveError):
            emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(before.parts), 'output_parts': 0,
                  'operations': 0, 'reopened': False})
            return
        try:
            refs = defaultdict(list)
            for r in after.relationships:
                if not r.external:
                    refs[_rels_path(r.source)].append(r.target)  # the relationship part holds the reference
            input_names = {p.name for p in before.parts}
            for p in after.parts:
                item = {'kind': 'output_part', 'part': p.name, 'content_type': p.content_type or 'application/octet-stream',
                        'references': sorted(set(refs.get(p.name, [])))}
                if p.name in input_names:
                    item['source'] = p.name
                emit(item)
                task.check()
                if p.role == 'uninspected':
                    emit({'kind': 'unverified', 'scope': p.name, 'reason': 'PART_NOT_INSPECTED'})
                elif p.role == 'structure':
                    # inspected again in the candidate itself, not taken from the writer's word
                    with out.open(p.name) as stream:
                        reason = inspect(stream)
                    if reason:
                        emit({'kind': 'unverified', 'scope': p.name, 'reason': reason})
            output_names = {p.name for p in after.parts}
            by_part: dict[str, dict[int, list[Op]]] = defaultdict(lambda: defaultdict(list))
            for op in ops:
                if op.kind == 'text':
                    by_part[op.part][op.ordinal].append(op)
            reopened = True
            checks = []
            for op in ops:
                if op.kind == 'part':
                    gone = op.part not in output_names
                    checks.append({'kind': 'op_check', 'op': op.id, 'executed': gone, 'residual': not gone, 'unit': op.part})
            for name, groups in by_part.items():
                if name not in output_names:
                    # the whole part is gone: its text operations are subsumed
                    for ops_ in groups.values():
                        for op in ops_:
                            checks.append({'kind': 'op_check', 'op': op.id, 'executed': True, 'residual': False, 'unit': f'{name}#{op.ordinal}'})
                    continue
                try:
                    old = _paragraphs(src, name, set(groups))
                    new = _paragraphs(out, name, set(groups))
                except XMLPartError:
                    reopened = False
                    continue
                for ordinal, ops_ in groups.items():
                    unit = f'{name}#{ordinal}'
                    before_text, after_text = old.get(ordinal), new.get(ordinal)
                    if before_text is None or after_text is None:
                        for op in ops_:
                            checks.append({'kind': 'op_check', 'op': op.id, 'executed': False, 'residual': False, 'unit': unit})
                        continue
                    verdict = judge(before_text, after_text, ops_)
                    for op in ops_:
                        executed, residual = verdict[op.id]
                        checks.append({'kind': 'op_check', 'op': op.id, 'executed': executed, 'residual': residual, 'unit': unit})
            order = {op.id: i for i, op in enumerate(ops)}
            for c in sorted(checks, key=lambda c: order[c['op']]):
                emit(c)
            emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(before.parts), 'output_parts': len(after.parts),
                  'operations': len(checks), 'reopened': reopened})
        finally:
            out.close()
    finally:
        src.close()

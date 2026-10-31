"""``format.write`` for the DOCX text subset: apply a plan, report what happened.

Every operation of the plan is reported as executed or not executed with a reason
code; the host fails the file if any approved operation is not executed. Every
input part is reported with its fate (kept, redacted, removed), which the host
reconciles against the re-enumerated candidate. Parts nobody asked to change are
copied byte for byte; text parts with operations are re-serialised through the
supported-subset rewriter, so anything outside that subset fails closed.
"""
from __future__ import annotations

import posixpath
import zipfile
from collections import defaultdict

from lxml import etree

from tacet_plugin_sdk.opc import PackageError
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.safezip import Archive, ArchiveError

from .package import inventory
from .plan import Op
from .xmlparts import Edit, XMLPartError, rewrite

_CT = '{http://schemas.openxmlformats.org/package/2006/content-types}'
_REL = '{http://schemas.openxmlformats.org/package/2006/relationships}'
MAX_META_BYTES = 16 << 20


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


def _parser():
    return etree.XMLParser(resolve_entities=False, no_network=True, load_dtd=False, dtd_validation=False, huge_tree=False)


def _read(archive, name):
    chunks, total = [], 0
    with archive.open(name) as stream:
        while block := stream.read(64 * 1024):
            total += len(block)
            if total > MAX_META_BYTES:
                raise TaskError('RESOURCE_LIMIT')
            chunks.append(block)
    return b''.join(chunks)


def _rels_path(source):
    if not source:
        return '_rels/.rels'
    return posixpath.join(posixpath.dirname(source), '_rels', posixpath.basename(source) + '.rels')


def _drop_relationships(data, ids):
    root = etree.fromstring(data, _parser())
    for node in list(root.iter(_REL + 'Relationship')):
        if node.get('Id') in ids:
            node.getparent().remove(node)
    return etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)


def _drop_overrides(data, names):
    root = etree.fromstring(data, _parser())
    for node in list(root.iter(_CT + 'Override')):
        if node.get('PartName', '').lstrip('/') in names:
            node.getparent().remove(node)
    return etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)


def write(source, ops: list[Op], sink, task, schemas) -> None:
    def emit(item):
        schemas.validate('format.schema.json', 'WriteItem', item)
        task.emit(item)

    try:
        archive = Archive(source)
    except ArchiveError:
        raise TaskError('INPUT_CORRUPT') from None
    out = _Counting(sink)
    try:
        try:
            inv = inventory(archive)
        except (PackageError, ArchiveError):
            raise TaskError('INPUT_CORRUPT') from None
        parts = {p.name: p for p in inv.parts}
        main = next(r.target for r in inv.relationships if not r.source and r.kind.endswith('/officeDocument') and not r.external)
        results: dict[str, tuple[str, str | None]] = {}
        text_ops: dict[str, dict[int, list[Op]]] = defaultdict(lambda: defaultdict(list))
        removed: dict[str, str] = {}
        for op in ops:
            part = parts.get(op.part)
            if op.kind == 'text':
                if part is None or part.role != 'text':
                    results[op.id] = ('not_executed', 'PART_NOT_TEXT')
                else:
                    text_ops[op.part][op.ordinal].append(op)
            elif part is None:
                results[op.id] = ('not_executed', 'PART_UNKNOWN')
            elif part.role in ('content_types', 'relationships') or part.name == main:
                results[op.id] = ('not_executed', 'PART_REQUIRED')
            else:
                removed[op.part] = op.id
        # a removed part takes its own relationship part with it
        gone = set(removed)
        for name in list(removed):
            own = _rels_path(name)
            if own in parts:
                gone.add(own)
        drops: dict[str, set[str]] = defaultdict(set)
        for rel in inv.relationships:
            if not rel.external and rel.target in removed:
                drops[_rels_path(rel.source)].add(rel.identifier)
        for name in list(drops):
            if name in gone:
                del drops[name]  # its owner is leaving anyway
        for op in ops:
            if op.kind == 'text' and op.part in removed:
                results[op.id] = ('executed', None)  # subsumed: the whole part is gone

        fates: list[tuple[str, str]] = []
        with zipfile.ZipFile(out, 'w', compression=zipfile.ZIP_DEFLATED, allowZip64=False) as zout:
            for entry in archive.entries:
                task.check()
                name = entry.name
                if name in gone:
                    fates.append((name, 'removed'))
                    if name in removed:
                        results[removed[name]] = ('executed', None)
                    continue
                if name == '[Content_Types].xml' and removed:
                    zout.writestr(name, _drop_overrides(_read(archive, name), set(removed)))
                    fates.append((name, 'redacted'))
                elif name in drops:
                    zout.writestr(name, _drop_relationships(_read(archive, name), drops[name]))
                    fates.append((name, 'redacted'))
                elif name in text_ops:
                    _rewrite_part(archive, zout, name, text_ops[name], results)
                    fates.append((name, 'redacted'))
                else:
                    with archive.open(name) as src, zout.open(name, 'w') as dst:
                        while chunk := src.read(64 * 1024):
                            dst.write(chunk)
                    fates.append((name, 'kept'))
        for op in ops:
            status, reason = results.get(op.id, ('not_executed', 'UNIT_NOT_FOUND'))
            item = {'kind': 'op_result', 'op': op.id, 'status': status}
            if reason:
                item['reason'] = reason
            emit(item)
        for name, fate in fates:
            emit({'kind': 'part_fate', 'part': name, 'fate': fate})
        emit({'kind': 'write_complete', 'complete': True, 'operations': len(ops), 'parts': len(fates), 'bytes': max(out.bytes, 1)})
    finally:
        archive.close()


def _rewrite_part(archive, zout, name, by_ordinal, results):
    applied: dict[str, bool] = {}
    wanted = [op for ops in by_ordinal.values() for op in ops]

    def decide(paragraph):
        ops = by_ordinal.get(paragraph.ordinal)
        if not ops:
            return []
        edits = []
        for op in ops:
            if op.end > len(paragraph.text):
                results[op.id] = ('not_executed', 'RANGE_INVALID')
                continue
            edits.append(Edit(op.start, op.end, op.replacement))
            applied[op.id] = True
        return edits

    try:
        with archive.open(name) as src, zout.open(name, 'w') as dst:
            rewrite(src, dst, decide)
    except XMLPartError as error:
        reason = str(error) if str(error).replace('_', '').isalnum() and str(error).isupper() else 'REWRITE_FAILED'
        for op in wanted:
            results[op.id] = ('not_executed', reason[:64])
        return
    for op in wanted:
        if op.id in applied and op.id not in results:
            results[op.id] = ('executed', None)

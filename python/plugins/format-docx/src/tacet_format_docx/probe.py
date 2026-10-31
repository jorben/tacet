"""``format.probe`` for DOCX: what the file really is, whether it is encrypted, how big.

The extension is not consulted; the signature and the package are. Nothing is
decrypted and nothing is extracted. ``format`` is ``docx`` only when the package
has a Word main part; any other readable input is ``unknown`` (a verdict the
host turns into "not supported"), never a guess. A damaged or over-limit
package is a failed probe, not a format verdict.
"""
from __future__ import annotations

import msoffcrypto
from msoffcrypto.format.ooxml import OOXMLFile

from tacet_plugin_sdk.opc import PackageError
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.safezip import Archive, ArchiveError

from .package import inventory

_OLE = b'\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1'
_ZIP = (b'PK\x03\x04', b'PK\x05\x06')


def _result(fmt, encryption, images=0, embedded=0, size=0):
    return {'kind': 'probe_result', 'format': fmt, 'encryption': encryption,
            'pages': 0, 'images': images, 'embedded': embedded, 'bytes': size}


def probe(source) -> dict:
    source.seek(0)
    head = source.read(8)
    source.seek(0)
    if head.startswith(_OLE):
        return _probe_ole(source)
    if head[:4] in _ZIP:
        return _probe_zip(source)
    return _result('unknown', 'none')


def _probe_ole(source) -> dict:
    try:
        office = msoffcrypto.OfficeFile(source)
        if not isinstance(office, OOXMLFile) or not office.is_encrypted():
            return _result('unknown', 'none')
        method = office.type
    except Exception:  # olefile, expat and struct errors alike: not a recognisable container
        return _result('unknown', 'none')
    if method in ('agile', 'standard'):
        return _result('docx', 'password_required')
    return _result('docx', 'unsupported')  # extensible (certificate / DRM) encryption


def _probe_zip(source) -> dict:
    try:
        archive = Archive(source)
    except ArchiveError:
        raise TaskError('INPUT_CORRUPT') from None
    try:
        try:
            parts = inventory(archive).parts
        except PackageError:
            # inventory demands a Word main part, so another OPC format (or a package
            # with unreadable metadata) is not something this plugin will claim
            return _result('unknown', 'none')
        images = sum(1 for p in parts if p.name.startswith('word/media/'))
        embedded = sum(1 for p in parts if p.name.startswith('word/embeddings/'))
        return _result('docx', 'none', images, embedded, sum(e.size for e in archive.entries))
    finally:
        archive.close()

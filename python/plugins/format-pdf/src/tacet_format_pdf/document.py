"""Opening PDF documents and mapping PDFium's load errors to protocol outcomes."""
from __future__ import annotations

import pypdfium2 as pdfium
import pypdfium2.raw as raw

from tacet_plugin_sdk.runtime import TaskError

MAX_PDF_BYTES = 256 << 20  # the unlock copy is built in memory; see unlock.py


def has_header(source) -> bool:
    source.seek(0)
    head = source.read(1024)
    source.seek(0)
    return b'%PDF-' in head


def classify(error: pdfium.PdfiumError) -> str:
    """password | unsupported | corrupt, from the load error code."""
    code = getattr(error, 'err_code', None)
    if code == raw.FPDF_ERR_PASSWORD:
        return 'password'
    if code == raw.FPDF_ERR_SECURITY:
        return 'unsupported'
    return 'corrupt'


def encrypted(document) -> bool:
    """True when the security handler is in use (a document opened without a password
    can still carry permission restrictions)."""
    return raw.FPDF_GetSecurityHandlerRevision(document) != -1


def size_of(source) -> int:
    source.seek(0, 2)
    size = source.tell()
    source.seek(0)
    return size


def require_size(source) -> int:
    size = size_of(source)
    if size > MAX_PDF_BYTES:
        raise TaskError('RESOURCE_LIMIT')
    return size

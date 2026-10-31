"""``format.probe`` for PDF: signature, encryption state, page count and counts of
images and embedded files. Nothing is decrypted, rendered or extracted."""
from __future__ import annotations

import pypdfium2 as pdfium
import pypdfium2.raw as raw

from tacet_plugin_sdk.runtime import TaskError

from .document import classify, encrypted, has_header, size_of
from .raster import Limits, OBJECT_DEPTH


def _result(fmt, encryption, pages=0, images=0, embedded=0, size=0):
    return {'kind': 'probe_result', 'format': fmt, 'encryption': encryption,
            'pages': pages, 'images': images, 'embedded': embedded, 'bytes': size}


def probe(source, limits: Limits = Limits()) -> dict:
    size = size_of(source)
    if not has_header(source):
        return _result('unknown', 'none', size=size)
    try:
        document = pdfium.PdfDocument(source)
    except pdfium.PdfiumError as error:
        kind = classify(error)
        if kind == 'password':
            return _result('pdf', 'password_required', size=size)
        if kind == 'unsupported':
            return _result('pdf', 'unsupported', size=size)
        raise TaskError('INPUT_CORRUPT') from None
    try:
        pages = len(document)
        encryption = 'permission_only' if encrypted(document) else 'none'
        embedded = document.count_attachments()
        images = 0
        if pages <= limits.pages:  # an over-limit document is refused on its page count alone
            seen = 0
            for index in range(pages):
                page = document[index]
                try:
                    for _ in page.get_objects(filter=[raw.FPDF_PAGEOBJ_IMAGE], max_depth=OBJECT_DEPTH):
                        images += 1
                        seen += 1
                        if seen > limits.objects:
                            raise TaskError('RESOURCE_LIMIT')
                finally:
                    page.close()
        return _result('pdf', encryption, pages, images, embedded, size)
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    finally:
        document.close()

"""``format.unlock`` for password-protected PDF: open with the password, save a copy
without the security handler (FPDF_REMOVE_SECURITY).

As for DOCX, the copy is built in memory and written to the sink only after it
has been reopened without a password and has the same page count, so a wrong
password or a damaged input leaves no partial output. Python strings cannot be
zeroed (architecture 4.6); no error carries the password.
"""
from __future__ import annotations

import io

import pypdfium2 as pdfium
import pypdfium2.raw as raw

from tacet_plugin_sdk.runtime import TaskError

from .document import classify, encrypted, require_size


def unlock(source, sink, password: str) -> int:
    require_size(source)
    try:
        plain = pdfium.PdfDocument(source)
    except pdfium.PdfiumError as error:
        kind = classify(error)
        if kind == 'unsupported':
            raise TaskError('ENCRYPTION_UNSUPPORTED') from None
        if kind == 'corrupt':
            raise TaskError('INPUT_CORRUPT') from None
    else:
        # opens without a password: nothing to unlock (a permission-only document needs none)
        plain.close()
        raise TaskError('NOT_ENCRYPTED')
    source.seek(0)
    try:
        try:
            document = pdfium.PdfDocument(source, password=password)
        except pdfium.PdfiumError as error:
            raise TaskError('PASSWORD_INCORRECT' if classify(error) == 'password' else 'ENCRYPTION_UNSUPPORTED') from None
        finally:
            password = None  # release the reference as early as possible
        try:
            pages = len(document)
            copy = io.BytesIO()
            document.save(copy, flags=raw.FPDF_REMOVE_SECURITY)
        finally:
            document.close()
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    data = copy.getbuffer()
    try:
        check = pdfium.PdfDocument(io.BytesIO(data))
        try:
            if len(check) != pages or encrypted(check) or len(data) == 0:
                raise TaskError('INPUT_CORRUPT')
        finally:
            check.close()
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    sink.write(data)
    return len(data)

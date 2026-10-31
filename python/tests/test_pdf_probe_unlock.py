import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from tacet_format_pdf.probe import probe
from tacet_format_pdf.unlock import unlock
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas

PW = 'correct horse'


def image_pdf(text='x'):
    image = ('<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8', b'\x80')
    content = ('<< ', f'BT /F1 12 Tf 20 100 Td ({text}) Tj ET q 50 0 0 50 100 100 cm /Im0 Do Q'.encode())
    return build([text], extra={40: image, 11: content}, resources='/XObject << /Im0 40 0 R >>')


def attachment_pdf():
    return build(['x'], catalog_extra='/Names << /EmbeddedFiles << /Names [(a.txt) 50 0 R] >> >>', extra={
        50: b'<< /Type /Filespec /F (a.txt) /EF << /F 51 0 R >> >>', 51: ('<< /Type /EmbeddedFile', b'attached')})


class FixtureTest(unittest.TestCase):
    def test_the_hand_built_security_handler_is_a_real_one(self):
        # the fixtures are only evidence if PDFium itself agrees with them
        data = build(['hello'], user=PW.encode())
        with self.assertRaises(pdfium.PdfiumError) as ctx:
            pdfium.PdfDocument(io.BytesIO(data))
        self.assertEqual(ctx.exception.err_code, 4)
        with self.assertRaises(pdfium.PdfiumError):
            pdfium.PdfDocument(io.BytesIO(data), password='wrong')
        document = pdfium.PdfDocument(io.BytesIO(data), password=PW)
        self.assertEqual(document[0].get_textpage().get_text_range().strip(), 'hello')
        document.close()


class ProbeTest(unittest.TestCase):
    def setUp(self):
        self.schemas = Schemas()
        self.schemas.compile('format.schema.json', 'ProbeResult')

    def run_probe(self, raw):
        result = probe(io.BytesIO(raw))
        self.schemas.validate('format.schema.json', 'ProbeResult', result)
        return result

    def test_plain_pages_and_sizes(self):
        raw = build(['a', 'b', 'c'])
        r = self.run_probe(raw)
        self.assertEqual((r['format'], r['encryption'], r['pages'], r['images'], r['embedded'], r['bytes']), ('pdf', 'none', 3, 0, 0, len(raw)))

    def test_password_protected_is_locked_without_being_opened(self):
        r = self.run_probe(build(['secret'], user=PW.encode()))
        self.assertEqual((r['format'], r['encryption'], r['pages']), ('pdf', 'password_required', 0))

    def test_permission_password_only_opens_and_is_reported_as_such(self):
        r = self.run_probe(build(['x', 'y'], user=b'', permissions=-3904))
        self.assertEqual((r['format'], r['encryption'], r['pages']), ('pdf', 'permission_only', 2))

    def test_an_unsupported_security_handler_is_unsupported_not_locked(self):
        raw = build(['x'], extra={30: b'<< /Filter /Adobe.PubSec /SubFilter /adbe.pkcs7.s4 /V 4 >>'}).replace(b'/Root 1 0 R', b'/Root 1 0 R /Encrypt 30 0 R')
        r = self.run_probe(raw)
        self.assertEqual((r['format'], r['encryption']), ('pdf', 'unsupported'))

    def test_images_and_embedded_files_are_counted(self):
        r = self.run_probe(image_pdf())
        self.assertEqual((r['images'], r['embedded']), (1, 0))
        r = self.run_probe(attachment_pdf())
        self.assertEqual((r['images'], r['embedded']), (0, 1))

    def test_other_inputs_are_unknown_or_a_failed_probe_never_a_guess(self):
        for name, raw in {'empty': b'', 'text': b'hello', 'docx zip': b'PK\x03\x04' + b'\x00' * 30}.items():
            with self.subTest(name):
                r = self.run_probe(raw)
                self.assertEqual((r['format'], r['encryption']), ('unknown', 'none'))
        with self.assertRaises(TaskError) as ctx:
            probe(io.BytesIO(b'%PDF-1.4\nthis is not a pdf at all'))
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')

    def test_the_page_limit_is_reported_not_enforced_here(self):
        from tacet_format_pdf.raster import Limits
        r = probe(io.BytesIO(build(['p'] * 5)), Limits(pages=3))
        self.assertEqual((r['pages'], r['images']), (5, 0))


class UnlockTest(unittest.TestCase):
    def setUp(self):
        self.locked = build(['page one 13812345678', 'page two'], user=PW.encode())

    def run_unlock(self, raw, password=PW):
        sink = io.BytesIO()
        n = unlock(io.BytesIO(raw), sink, password)
        return n, sink.getvalue()

    def test_correct_password_gives_an_openable_copy_without_security_and_the_same_text(self):
        n, copy = self.run_unlock(self.locked)
        self.assertEqual(n, len(copy))
        document = pdfium.PdfDocument(io.BytesIO(copy))
        self.assertEqual(len(document), 2)
        self.assertIn('13812345678', document[0].get_textpage().get_text_range())
        self.assertNotIn(b'/Encrypt', copy)
        document.close()

    def test_wrong_password_leaves_no_output_and_the_error_never_quotes_it(self):
        sink = io.BytesIO()
        with self.assertRaises(TaskError) as ctx:
            unlock(io.BytesIO(self.locked), sink, 'hunter2-wrong')
        self.assertEqual(ctx.exception.code, 'PASSWORD_INCORRECT')
        self.assertEqual(sink.getvalue(), b'')
        self.assertNotIn('hunter2', str(ctx.exception) + repr(ctx.exception.__cause__) + repr(ctx.exception.__context__))

    def test_documents_that_need_no_password_are_refused_not_copied(self):
        for name, raw in {'plain': build(['x']), 'permission only': build(['x'], user=b'', permissions=-3904)}.items():
            with self.subTest(name):
                sink = io.BytesIO()
                with self.assertRaises(TaskError) as ctx:
                    unlock(io.BytesIO(raw), sink, PW)
                self.assertEqual(ctx.exception.code, 'NOT_ENCRYPTED')
                self.assertEqual(sink.getvalue(), b'')

    def test_unsupported_and_damaged_inputs(self):
        pubsec = build(['x'], extra={30: b'<< /Filter /Adobe.PubSec /V 4 >>'}).replace(b'/Root 1 0 R', b'/Root 1 0 R /Encrypt 30 0 R')
        for name, raw, code in (('certificate', pubsec, 'ENCRYPTION_UNSUPPORTED'), ('garbage', b'%PDF-1.4\nnonsense', 'INPUT_CORRUPT'), ('empty', b'', 'INPUT_CORRUPT')):
            with self.subTest(name):
                sink = io.BytesIO()
                with self.assertRaises(TaskError) as ctx:
                    unlock(io.BytesIO(raw), sink, PW)
                self.assertEqual(ctx.exception.code, code)
                self.assertEqual(sink.getvalue(), b'')

    def test_an_oversized_input_is_refused_before_it_is_parsed(self):
        import tacet_format_pdf.document as module
        original = module.MAX_PDF_BYTES
        module.MAX_PDF_BYTES = 100
        try:
            with self.assertRaises(TaskError) as ctx:
                self.run_unlock(self.locked)
            self.assertEqual(ctx.exception.code, 'RESOURCE_LIMIT')
        finally:
            module.MAX_PDF_BYTES = original

    def test_the_copy_must_reopen_without_a_password_or_nothing_is_written(self):
        import tacet_format_pdf.unlock as module
        original = module.raw.FPDF_REMOVE_SECURITY
        module.raw = type('R', (), {'FPDF_REMOVE_SECURITY': 0})  # keep the security handler on save
        sink = io.BytesIO()
        try:
            with self.assertRaises(TaskError) as ctx:
                unlock(io.BytesIO(self.locked), sink, PW)
        finally:
            import pypdfium2.raw as raw
            module.raw = raw
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')
        self.assertEqual(sink.getvalue(), b'')


if __name__ == '__main__':
    unittest.main()

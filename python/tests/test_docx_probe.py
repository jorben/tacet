import io
import unittest
import zipfile

from tacet_format_docx.probe import probe
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas
from test_docx_extract import package
from test_docx_unlock import encrypted, sized


def with_parts(raw: bytes, extra: dict) -> bytes:
    out = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(raw)) as zin, zipfile.ZipFile(out, 'w', zipfile.ZIP_STORED) as zout:
        for name in zin.namelist():
            zout.writestr(name, zin.read(name))
        for name, data in extra.items():
            zout.writestr(name, data)
    return out.getvalue()


class ProbeTest(unittest.TestCase):
    def setUp(self):
        self.plain = package('<w:p><w:r><w:t>x</w:t></w:r></w:p>')
        self.schemas = Schemas()
        self.schemas.compile('format.schema.json', 'ProbeResult')

    def run_probe(self, raw):
        result = probe(io.BytesIO(raw))
        self.schemas.validate('format.schema.json', 'ProbeResult', result)
        return result

    def test_a_plain_package_reports_format_media_embeddings_and_expanded_size(self):
        raw = with_parts(self.plain, {'word/media/a.png': b'1' * 100, 'word/media/b.png': b'2' * 50, 'word/embeddings/o.bin': b'3' * 10})
        r = self.run_probe(raw)
        self.assertEqual((r['format'], r['encryption'], r['images'], r['embedded']), ('docx', 'none', 2, 1))
        with zipfile.ZipFile(io.BytesIO(raw)) as z:
            self.assertEqual(r['bytes'], sum(i.file_size for i in z.infolist()))

    def test_a_password_protected_package_is_recognised_without_being_decrypted(self):
        r = self.run_probe(encrypted(sized(self.plain)))
        self.assertEqual((r['format'], r['encryption']), ('docx', 'password_required'))
        self.assertEqual((r['images'], r['bytes']), (0, 0))

    def test_the_extension_is_not_consulted_only_the_content(self):
        # the probe sees bytes only; a spreadsheet package is not claimed as docx
        out = io.BytesIO()
        with zipfile.ZipFile(out, 'w') as z:
            z.writestr('[Content_Types].xml', '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>')
            z.writestr('xl/workbook.xml', '<x/>')
        self.assertEqual(self.run_probe(out.getvalue())['format'], 'unknown')

    def test_unrecognised_inputs_are_unknown_not_guessed(self):
        for name, raw in {'empty': b'', 'text': b'hello world', 'pdf': b'%PDF-1.7\n', 'bare ole': b'\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1' + b'\x00' * 600,
                          'tiny zip header': b'PK\x05\x06' + b'\x00' * 18}.items():
            with self.subTest(name):
                try:
                    r = self.run_probe(raw)
                except TaskError as error:
                    self.assertEqual(error.code, 'INPUT_CORRUPT')
                    continue
                self.assertEqual((r['format'], r['encryption']), ('unknown', 'none'))

    def test_a_damaged_zip_is_a_failed_probe_not_a_format_verdict(self):
        raw = self.plain[:len(self.plain) // 2] + b'PK\x03\x04'
        with self.assertRaises(TaskError) as ctx:
            probe(io.BytesIO(raw))
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')

    def test_a_certificate_encrypted_container_is_unsupported_not_locked(self):
        import tacet_format_docx.probe as module
        original = module.msoffcrypto.OfficeFile

        class Fake(module.OOXMLFile):
            def __init__(self, source):
                self.type = 'extensible'

            def is_encrypted(self):
                return True
        module.msoffcrypto.OfficeFile = Fake
        try:
            r = probe(io.BytesIO(b'\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1'))
        finally:
            module.msoffcrypto.OfficeFile = original
        self.assertEqual((r['format'], r['encryption']), ('docx', 'unsupported'))

    def _with_office(self, factory):
        import tacet_format_docx.probe as module
        original = module.msoffcrypto.OfficeFile
        module.msoffcrypto.OfficeFile = factory
        try:
            return probe(io.BytesIO(b'\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1'))
        finally:
            module.msoffcrypto.OfficeFile = original

    def test_an_ole_container_that_is_not_an_encrypted_ooxml_package_is_not_claimed(self):
        class LegacyDoc:  # e.g. a binary .doc: an OLE file, but not an OOXML package
            type = 'agile'

            def __init__(self, source):
                pass

            def is_encrypted(self):
                return True

        from tacet_format_docx.probe import OOXMLFile

        class Unencrypted(OOXMLFile):
            type = 'agile'

            def __init__(self, source):
                pass

            def is_encrypted(self):
                return False

        for factory in (LegacyDoc, Unencrypted):
            with self.subTest(factory.__name__):
                r = self._with_office(factory)
                self.assertEqual((r['format'], r['encryption']), ('unknown', 'none'))


if __name__ == '__main__':
    unittest.main()

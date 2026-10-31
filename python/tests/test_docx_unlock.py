import io
import unittest
import zipfile

import msoffcrypto
from msoffcrypto.format.ooxml import OOXMLFile

from tacet_format_docx.extract import extract
from tacet_format_docx.unlock import unlock
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas
from test_docx_extract import FakeTask, package

PASSWORD = 'correct horse'

# Fixtures are encrypted by msoffcrypto itself, not by Word or WPS (that evidence
# is still outstanding). Its CFB writer corrupts streams under 4096 bytes, so
# every fixture is padded past that with an incompressible part.
FILLER = 8192


def sized(plain: bytes) -> bytes:
    import os
    out = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(plain)) as zin, zipfile.ZipFile(out, 'w', zipfile.ZIP_STORED) as zout:
        for name in zin.namelist():
            zout.writestr(name, zin.read(name))
        zout.writestr('word/media/filler.bin', os.urandom(FILLER))
    return out.getvalue()


def encrypted(plain: bytes, password=PASSWORD) -> bytes:
    out = io.BytesIO()
    OOXMLFile(io.BytesIO(plain)).encrypt(password, out)
    return out.getvalue()


def run_unlock(raw, password):
    sink = io.BytesIO()
    n = unlock(io.BytesIO(raw), sink, password)
    return n, sink.getvalue()


class UnlockTest(unittest.TestCase):
    def setUp(self):
        self.plain = sized(package('<w:p><w:r><w:t>联系人张三 13812345678</w:t></w:r></w:p>'))

    def test_correct_password_yields_a_valid_package_with_the_same_text(self):
        raw = encrypted(self.plain)
        self.assertNotIn(b'13812345678', raw)
        self.assertFalse(zipfile.is_zipfile(io.BytesIO(raw)))  # an OLE container, not a zip
        n, copy = run_unlock(raw, PASSWORD)
        self.assertEqual(n, len(copy))
        self.assertTrue(zipfile.is_zipfile(io.BytesIO(copy)))
        schemas = Schemas()
        schemas.compile('format.schema.json', 'ExtractItem')
        task = FakeTask()
        extract(io.BytesIO(copy), task, schemas)
        self.assertEqual([i['text'] for i in task.items if i['kind'] == 'text_unit'], ['联系人张三 13812345678'])

    def test_it_works_on_the_sdk_resource_reader_and_writer_not_only_on_memory_files(self):
        import os
        from tacet_plugin_sdk.resources import ResourceReader, ResourceWriter
        key = os.urandom(32)
        stored = io.BytesIO()
        writer = ResourceWriter(stored, key, 'in', 1 << 28)
        writer.write(encrypted(self.plain))
        writer.close()
        stored.seek(0)
        reader = ResourceReader(stored, key, 'in')
        out = io.BytesIO()
        sink = ResourceWriter(out, key, 'out', 1 << 28)
        n = unlock(reader, sink, PASSWORD)
        sink.close()
        out.seek(0)
        check = ResourceReader(out, key, 'out')
        copy = b''.join(iter(lambda: check.read(65536), b''))
        self.assertEqual(n, len(copy))
        self.assertTrue(zipfile.is_zipfile(io.BytesIO(copy)))

    def test_wrong_password_is_reported_as_such_and_writes_no_usable_output(self):
        sink = io.BytesIO()
        with self.assertRaises(TaskError) as ctx:
            unlock(io.BytesIO(encrypted(self.plain)), sink, 'wrong password')
        self.assertEqual(ctx.exception.code, 'PASSWORD_INCORRECT')
        self.assertEqual(sink.getvalue(), b'')
        self.assertNotIn('wrong password', str(ctx.exception))

    def test_a_package_that_is_not_encrypted_is_refused_not_copied(self):
        sink = io.BytesIO()
        with self.assertRaises(TaskError) as ctx:
            unlock(io.BytesIO(self.plain), sink, PASSWORD)
        self.assertEqual(ctx.exception.code, 'NOT_ENCRYPTED')
        self.assertEqual(sink.getvalue(), b'')

    def test_input_that_is_not_an_office_file_is_an_unsupported_encryption(self):
        for junk in (b'', b'not an office file', b'\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1' + b'\x00' * 64, b'PK\x03\x04' + b'\x00' * 40):
            with self.subTest(junk=junk[:12]):
                with self.assertRaises(TaskError) as ctx:
                    run_unlock(junk, PASSWORD)
                self.assertIn(ctx.exception.code, {'ENCRYPTION_UNSUPPORTED', 'NOT_ENCRYPTED'})

    def test_damage_anywhere_is_an_error_or_the_exact_original_never_other_content(self):
        raw = encrypted(self.plain)
        _, good = run_unlock(raw, PASSWORD)
        outcomes = set()
        step = max(1, len(raw) // 97)
        for pos in range(0, len(raw), step):
            damaged = bytearray(raw)
            damaged[pos] ^= 0x01
            sink = io.BytesIO()
            try:
                unlock(io.BytesIO(bytes(damaged)), sink, PASSWORD)
            except TaskError as error:
                outcomes.add(error.code)
                self.assertEqual(sink.getvalue(), b'', f'partial output at {pos}')
                continue
            outcomes.add('ok')
            self.assertEqual(sink.getvalue(), good, f'altered content accepted at {pos}')
        self.assertIn('INPUT_CORRUPT', outcomes)  # payload damage is caught by the integrity check
        self.assertLessEqual(outcomes - {'ok'}, {'INPUT_CORRUPT', 'ENCRYPTION_UNSUPPORTED', 'PASSWORD_INCORRECT'})

    def test_damage_after_a_verified_password_is_corruption_not_a_wrong_password(self):
        raw = bytearray(encrypted(self.plain))
        raw[len(raw) // 2] ^= 0xFF  # the payload dominates the file; the tail is sector padding
        with self.assertRaises(TaskError) as ctx:
            run_unlock(bytes(raw), PASSWORD)
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')

    def test_a_validly_encrypted_payload_that_is_not_a_package_is_refused_without_output(self):
        from msoffcrypto.method.ecma376_agile import ECMA376Agile
        raw = ECMA376Agile.encrypt(PASSWORD, io.BytesIO(b'not a zip package ' * 400))
        sink = io.BytesIO()
        with self.assertRaises(TaskError) as ctx:
            unlock(io.BytesIO(raw), sink, PASSWORD)
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')
        self.assertEqual(sink.getvalue(), b'')

    def test_an_oversized_input_is_refused_before_it_is_parsed(self):
        import tacet_format_docx.unlock as module
        original = module.MAX_ENCRYPTED_BYTES
        module.MAX_ENCRYPTED_BYTES = 100
        try:
            with self.assertRaises(TaskError) as ctx:
                run_unlock(encrypted(self.plain), PASSWORD)
            self.assertEqual(ctx.exception.code, 'RESOURCE_LIMIT')
        finally:
            module.MAX_ENCRYPTED_BYTES = original

    def test_no_error_ever_contains_the_password(self):
        for raw, password in ((encrypted(self.plain), 'hunter2-secret'), (self.plain, 'hunter2-secret'), (b'junk', 'hunter2-secret')):
            try:
                run_unlock(raw, password)
            except TaskError as error:
                self.assertNotIn('hunter2', str(error))
                self.assertNotIn('hunter2', repr(error.__cause__))


if __name__ == '__main__':
    unittest.main()

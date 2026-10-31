import hashlib
import io
import json
import unittest
from pathlib import Path

from tacet_plugin_sdk.resources import ResourceError, ResourceReader, ResourceWriter, HEADER_BYTES, RECORD_BYTES, SEGMENT_BYTES


KEY = b'k' * 32
IDENTITY = 'synthetic-resource'


def encode(data):
    output = io.BytesIO()
    writer = ResourceWriter(output, KEY, IDENTITY, max(1, len(data)))
    for offset in range(0, len(data), 1373):
        writer.write(data[offset:offset+1373])
    writer.close()
    return output.getvalue()


class ResourceTest(unittest.TestCase):
    def test_boundaries_and_random_reads(self):
        for size in (0, 1, SEGMENT_BYTES-1, SEGMENT_BYTES, SEGMENT_BYTES+1, 3*SEGMENT_BYTES):
            with self.subTest(size=size):
                data = (b'generated-private-marker' * (size // 24 + 2))[:size]
                reader = ResourceReader(io.BytesIO(encode(data)), KEY, IDENTITY)
                reader.verify()
                self.assertEqual(reader.size, size)
                for offset in (0, min(7, size), min(SEGMENT_BYTES-1, size), size):
                    reader.seek(offset)
                    self.assertEqual(reader.read(37), data[offset:offset+37])
                reader.close()

    def test_tamper_swap_and_truncate(self):
        encoded = encode(b'private-test-value' * 20_000)
        self.assertNotIn(b'private-test-value', encoded)
        mutated = bytearray(encoded)
        mutated[HEADER_BYTES+5] ^= 1
        swapped = bytearray(encoded)
        swapped[HEADER_BYTES:HEADER_BYTES+RECORD_BYTES], swapped[HEADER_BYTES+RECORD_BYTES:HEADER_BYTES+2*RECORD_BYTES] = (
            swapped[HEADER_BYTES+RECORD_BYTES:HEADER_BYTES+2*RECORD_BYTES], swapped[HEADER_BYTES:HEADER_BYTES+RECORD_BYTES])
        for corrupt in (bytes(mutated), bytes(swapped), encoded[:-1], encoded[:HEADER_BYTES+RECORD_BYTES], encoded+b'!'):
            with self.subTest(size=len(corrupt)), self.assertRaises(ResourceError):
                reader = ResourceReader(io.BytesIO(corrupt), KEY, IDENTITY)
                reader.verify()

    def test_wrong_key_identity_version_and_closed_reader(self):
        encoded = encode(b'data')
        for key, identity in ((b'x'*32, IDENTITY), (KEY, 'other')):
            with self.assertRaises(ResourceError):
                ResourceReader(io.BytesIO(encoded), key, identity)
        changed = bytearray(encoded)
        changed[9] = 2
        with self.assertRaisesRegex(ResourceError, 'RESOURCE_VERSION_UNSUPPORTED'):
            ResourceReader(io.BytesIO(changed), KEY, IDENTITY)
        reader = ResourceReader(io.BytesIO(encoded), KEY, IDENTITY)
        reader.close()
        with self.assertRaisesRegex(ResourceError, 'RESOURCE_CLOSED'):
            reader.read(1)

    def test_rewrite_has_fresh_key_and_quota_failure_cannot_seal(self):
        self.assertNotEqual(encode(b'data'), encode(b'data'))
        writer = ResourceWriter(io.BytesIO(), KEY, IDENTITY, 1)
        with self.assertRaisesRegex(ResourceError, 'RESOURCE_QUOTA'):
            writer.write(b'too large')
        with self.assertRaisesRegex(ResourceError, 'RESOURCE_CLOSED'):
            writer.close()

    def test_reads_are_bounded(self):
        reader = ResourceReader(io.BytesIO(encode(b'data')), KEY, IDENTITY)
        for size in (-1, 1024*1024+1):
            with self.assertRaisesRegex(ResourceError, 'RESOURCE_READ_LIMIT'):
                reader.read(size)

    def test_zipfile_reads_directly_from_authenticated_random_access_stream(self):
        import zipfile
        plain = io.BytesIO()
        with zipfile.ZipFile(plain, 'w', compression=zipfile.ZIP_DEFLATED) as package:
            package.writestr('part.xml', b'<generated>synthetic</generated>')
        reader = ResourceReader(io.BytesIO(encode(plain.getvalue())), KEY, IDENTITY)
        self.assertTrue(reader.seekable())
        self.assertTrue(reader.readable())
        self.assertFalse(reader.writable())
        with zipfile.ZipFile(reader) as package:
            self.assertEqual(package.read('part.xml'), b'<generated>synthetic</generated>')
        reader.close()
        self.assertFalse(reader.seekable())

    def test_shared_go_python_vectors(self):
        root = Path(__file__).resolve().parents[2]
        vectors = json.loads((root / 'protocol/testdata/resource-v1.json').read_text())
        for vector in vectors:
            with self.subTest(name=vector['name']):
                reader = ResourceReader(io.BytesIO(bytes.fromhex(vector['encoded_hex'])),
                                        bytes.fromhex(vector['key_hex']), vector['resource_id'])
                reader.verify()
                self.assertEqual(reader.read(1024), bytes.fromhex(vector['plaintext_hex']))


if __name__ == '__main__':
    unittest.main()

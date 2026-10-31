import io
import struct
import unittest
import warnings
import zipfile

from tacet_plugin_sdk.safezip import Archive, ArchiveError, Limits, inspect


def package(entries, *, streaming=False):
    class Sink(io.BytesIO):
        def seekable(self):
            return False

        def seek(self, *args):
            raise io.UnsupportedOperation()

    stream = Sink() if streaming else io.BytesIO()
    with zipfile.ZipFile(stream, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)
            for name, data in entries:
                archive.writestr(name, data)
    return stream.getvalue()


class SafeZipTest(unittest.TestCase):
    def reject(self, data, code, limits=Limits()):
        with self.assertRaisesRegex(ArchiveError, "^" + code + "$"):
            inspect(io.BytesIO(data), limits)

    def test_inventory_includes_unknown_and_unreferenced_parts(self):
        raw = package([("[Content_Types].xml", b"<Types/>"), ("word/document.xml", b"<doc/>"),
                       ("unknown/orphan.bin", b"secret")])
        entries = inspect(io.BytesIO(raw))
        self.assertEqual([e.name for e in entries],
                         ["[Content_Types].xml", "word/document.xml", "unknown/orphan.bin"])
        archive = Archive(io.BytesIO(raw))
        try:
            with archive.open("unknown/orphan.bin") as source:
                self.assertEqual(source.read(64), b"secret")
                self.assertEqual(source.read(1), b"")
        finally:
            archive.close()

    def test_supports_append_only_zip_output_descriptors(self):
        raw = package([("word/document.xml", b"a" * 500)], streaming=True)
        entry, = inspect(io.BytesIO(raw))
        self.assertTrue(entry.flags & 8)
        self.assertEqual(entry.size, 500)

    def test_duplicate_names_rejected_before_zipfile_selects_one(self):
        self.reject(package([("a.xml", b"first"), ("a.xml", b"second")]), "ZIP_DUPLICATE_ENTRY")

    def test_path_traversal_and_platform_aliases_rejected(self):
        for name in ("../secret", "/secret", "a/../secret", "C:secret", "a\\secret", "./secret"):
            with self.subTest(name=name):
                self.reject(package([(name, b"x")]), "ZIP_UNSAFE_NAME")

    def test_declared_expansion_and_directory_budgets(self):
        raw = package([("a", b"x" * 100), ("b", b"y")])
        self.reject(raw, "ZIP_DIRECTORY_LIMIT", Limits(entries=1))
        self.reject(raw, "ZIP_EXPANSION_LIMIT", Limits(expanded_bytes=99))
        self.reject(raw, "ZIP_EXPANSION_LIMIT", Limits(entry_bytes=99))
        self.reject(raw, "ZIP_RATIO_LIMIT", Limits(ratio=1))

    def test_local_name_and_sizes_cannot_disagree(self):
        raw = bytearray(package([("a", b"safe")]))
        raw[30] = ord("b")
        self.reject(raw, "ZIP_LOCAL_MISMATCH")
        raw = bytearray(package([("a", b"safe")]))
        struct.pack_into("<I", raw, 22, 17)
        self.reject(raw, "ZIP_LOCAL_MISMATCH")

    def test_encryption_flags_fail_before_read(self):
        raw = bytearray(package([("a", b"safe")]))
        directory = raw.index(b"PK\x01\x02")
        struct.pack_into("<H", raw, 6, 1)
        struct.pack_into("<H", raw, directory + 8, 1)
        self.reject(raw, "ZIP_FLAGS_UNSUPPORTED")

    def test_aliasing_two_central_entries_to_one_local_header(self):
        raw = bytearray(package([("a", b"same"), ("b", b"same")]))
        first = raw.index(b"PK\x01\x02")
        second = raw.index(b"PK\x01\x02", first + 1)
        struct.pack_into("<I", raw, second + 42, 0)
        self.reject(raw, "ZIP_LOCAL_MISMATCH")

    def test_streaming_descriptor_must_match_central_facts(self):
        raw = bytearray(package([("a", b"safe")], streaming=True))
        descriptor = raw.index(b"PK\x07\x08")
        struct.pack_into("<I", raw, descriptor + 4, 0)
        self.reject(raw, "ZIP_DESCRIPTOR_MISMATCH")

    def test_unregistered_trailing_or_prepended_bytes_rejected(self):
        raw = package([("a", b"safe")])
        self.reject(raw + b"hidden", "ZIP_TRAILING_DATA")
        self.reject(b"executable" + raw, "ZIP_DIRECTORY_MISMATCH")

    def test_crc_corruption_does_not_return_content_as_valid(self):
        raw = bytearray(package([("a", b"safe")]))
        entry, = inspect(io.BytesIO(raw))
        # Corrupt the CRC consistently in both headers; inflate still succeeds.
        struct.pack_into("<I", raw, 14, 0)
        struct.pack_into("<I", raw, raw.index(b"PK\x01\x02") + 16, 0)
        archive = Archive(io.BytesIO(raw))
        try:
            with self.assertRaisesRegex(ArchiveError, "ZIP_ENTRY_CORRUPT"):
                with archive.open("a") as source:
                    source.read(64)
        finally:
            archive.close()

    def test_no_unbounded_read_interface(self):
        archive = Archive(io.BytesIO(package([("a", b"safe")])))
        try:
            with archive.open("a") as source:
                with self.assertRaisesRegex(ArchiveError, "ZIP_READ_LIMIT"):
                    source.read()
        finally:
            archive.close()


if __name__ == "__main__":
    unittest.main()

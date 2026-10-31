import hashlib
import tempfile
import unittest
from pathlib import Path

from tacet_detect_astrlink.integrity import IntegrityError, verify


class ModelIntegrityTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'model.onnx').write_bytes(b'fixed-test-graph')
        self.manifest = {
            'revision': 'a' * 40,
            'files': {'model.onnx': {'size': 16, 'sha256': hashlib.sha256(b'fixed-test-graph').hexdigest()}}
        }

    def test_exact_content_passes(self):
        verify(self.root, self.manifest)

    def test_missing_truncated_and_tampered_files_fail(self):
        for data, code in ((b'short', 'MODEL_SIZE_MISMATCH'),
                           (b'x' * 17, 'MODEL_SIZE_MISMATCH'),
                           (b'x' * 16, 'MODEL_DIGEST_MISMATCH')):
            with self.subTest(data=data):
                (self.root / 'model.onnx').write_bytes(data)
                with self.assertRaisesRegex(IntegrityError, code):
                    verify(self.root, self.manifest)
        (self.root / 'model.onnx').unlink()
        with self.assertRaisesRegex(IntegrityError, 'MODEL_FILE_MISSING'):
            verify(self.root, self.manifest)

    def test_symlink_and_manifest_path_escape_fail(self):
        path = self.root / 'model.onnx'
        path.rename(self.root / 'actual')
        path.symlink_to(self.root / 'actual')
        with self.assertRaisesRegex(IntegrityError, 'MODEL_FILE_MISSING'):
            verify(self.root, self.manifest)
        entry = self.manifest['files'].pop('model.onnx')
        self.manifest['files']['../model.onnx'] = entry
        with self.assertRaisesRegex(IntegrityError, 'MODEL_MANIFEST_INVALID'):
            verify(self.root, self.manifest)

    def test_mutable_revision_and_empty_catalog_fail(self):
        self.manifest['revision'] = 'main'
        with self.assertRaisesRegex(IntegrityError, 'MODEL_REVISION_INVALID'):
            verify(self.root, self.manifest)
        self.manifest['revision'] = 'a' * 40
        self.manifest['files'] = {}
        with self.assertRaisesRegex(IntegrityError, 'MODEL_MANIFEST_INVALID'):
            verify(self.root, self.manifest)


if __name__ == '__main__':
    unittest.main()

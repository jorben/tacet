import json
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MODEL = ROOT / '.pi/m0-resources/privacy-filter-fp16'
MANIFEST = ROOT / 'resources/models/privacy-filter-fp16.json'


@unittest.skipUnless((MODEL / 'model_fp16.onnx_data_1').exists(), 'FP16 Privacy Filter resource not downloaded')
class PrivacyFilterFP16Test(unittest.TestCase):
    def test_gpu_preference_and_forced_cpu_share_revision_and_find_the_same_sensitive_values(self):
        from tacet_detect_privacy_filter.detector import Detector
        manifest = json.loads(MANIFEST.read_text())
        text = 'Contact Jane Doe at jane.doe@example.org or +1 415-555-0132.'
        def found(detector):
            return {(s.label, text[s.start:s.end]) for s in detector.detect(text) if s.score >= 0.5}
        with tempfile.TemporaryDirectory() as tmp:
            gpu = Detector(MODEL, manifest, backend='auto', cache_dir=Path(tmp))
            gpu_found, revision = found(gpu), gpu.revision
            self.assertIn(gpu.backend, ['CoreMLExecutionProvider', 'DmlExecutionProvider', 'CUDAExecutionProvider',
                                        'ROCMExecutionProvider', 'CPUExecutionProvider'])
            del gpu
            cpu = Detector(MODEL, manifest, backend='CPUExecutionProvider')
            self.assertEqual(cpu.backend, 'CPUExecutionProvider')
            self.assertEqual(cpu.revision, revision)
            self.assertEqual(found(cpu), gpu_found)
            self.assertTrue({('private_person', 'Jane Doe'), ('private_email', 'jane.doe@example.org'),
                             ('private_phone', '+1 415-555-0132')}.issubset(gpu_found))

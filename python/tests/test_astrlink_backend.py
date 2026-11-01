import json
import tempfile
import unittest
from pathlib import Path

from tacet_detect_astrlink.detector import BackendError, graph_of, prepare_cache, providers_for
from tacet_detect_astrlink.integrity import IntegrityError

ROOT = Path(__file__).resolve().parents[2]
INT8_DIR = ROOT / '.pi/m0-resources/astrlink-guard-int8'
FP16_DIR = ROOT / '.pi/m0-resources/astrlink-guard-fp16'
SAMPLES = [
    '请联系张伟，电话 13812345678，邮箱 zhangwei@example.com，收货地址：北京市海淀区中关村大街 27 号。',
    '王芳的生日是 1992 年 7 月 8 日，护照有效期至 2031-05-01。',
    '公开示例 document ' * 300 + '联系人张伟，电话 13812345678。',
]


def manifest(name):
    return json.loads((ROOT / 'resources/models' / name).read_text())


def coreml_available():
    try:
        import onnxruntime
    except ImportError:
        return False
    return 'CoreMLExecutionProvider' in onnxruntime.get_available_providers()


class BackendOptionsTest(unittest.TestCase):
    def test_graph_is_the_one_onnx_file_of_the_manifest(self):
        self.assertEqual(graph_of(manifest('astrlink-guard-int8.json')), 'model_int8.onnx')
        self.assertEqual(graph_of(manifest('astrlink-guard-fp16.json')), 'model_fp16_static512.onnx')
        for files in ({'a.json': {}}, {'a.onnx': {}, 'b.onnx': {}}):
            with self.subTest(files=files), self.assertRaises(IntegrityError):
                graph_of({'files': files})

    def test_cpu_backend_is_the_only_provider(self):
        self.assertEqual(providers_for('CPUExecutionProvider', None), ['CPUExecutionProvider'])

    def test_coreml_needs_a_cache_and_caches_the_compiled_model(self):
        with self.assertRaises(BackendError):
            providers_for('CoreMLExecutionProvider', None)
        (name, options), fallback = providers_for('CoreMLExecutionProvider', Path('/c'))
        self.assertEqual((name, fallback), ('CoreMLExecutionProvider', 'CPUExecutionProvider'))
        self.assertEqual(options, {'MLComputeUnits': 'CPUAndGPU', 'ModelFormat': 'MLProgram', 'ModelCacheDirectory': '/c'})

    def test_cache_folder_is_private_per_runtime_version_and_never_a_link(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = prepare_cache(Path(tmp) / 'k', '1.31.1')
            self.assertEqual(path.name, 'ort-1.31.1')
            self.assertEqual(path.stat().st_mode & 0o777, 0o700)
            link = Path(tmp) / 'link'
            link.symlink_to(Path(tmp) / 'k')
            with self.assertRaises(BackendError):
                prepare_cache(link, '1.31.1')
            (Path(tmp) / 'k2').mkdir()
            (Path(tmp) / 'k2' / 'ort-1').symlink_to(tmp)
            with self.assertRaises(BackendError):
                prepare_cache(Path(tmp) / 'k2', '1')

    @unittest.skipUnless((INT8_DIR / 'model_int8.onnx').exists(), 'fixed M0 model resource not downloaded')
    def test_a_backend_the_runtime_lacks_is_refused_not_replaced(self):
        from tacet_detect_astrlink.detector import Detector
        m = manifest('astrlink-guard-int8.json')
        m['backend'] = 'NoSuchExecutionProvider'
        with self.assertRaises(BackendError):
            Detector(INT8_DIR, m)


@unittest.skipUnless((FP16_DIR / 'model_fp16_static512.onnx').exists() and (INT8_DIR / 'model_int8.onnx').exists()
                     and coreml_available(), 'needs both model resources and Core ML')
class AcceleratedModelTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        from tacet_detect_astrlink.detector import Detector
        cls.tmp = tempfile.TemporaryDirectory()
        cls.cpu = Detector(FP16_DIR, manifest('astrlink-guard-fp16.json'), backend='CPUExecutionProvider')
        cls.gpu = Detector(FP16_DIR, manifest('astrlink-guard-fp16.json'), cache_dir=Path(cls.tmp.name))

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_runs_on_core_ml_with_padded_static_windows(self):
        self.assertEqual(self.gpu.backend, 'CoreMLExecutionProvider')
        self.assertEqual(self.gpu._session.get_providers()[0], 'CoreMLExecutionProvider')
        self.assertEqual(self.gpu._static, 512)
        self.assertEqual(self.cpu._static, 512)
        self.assertEqual(self.cpu.revision, self.gpu.revision)

    def test_finds_what_the_cpu_baseline_finds(self):
        for text in SAMPLES:
            with self.subTest(text=text[:16]):
                want = [(s.label, s.start, s.end) for s in self.cpu.detect(text) if s.score >= 0.8]
                got = [(s.label, s.start, s.end) for s in self.gpu.detect(text) if s.score >= 0.8]
                self.assertEqual(got, want)


if __name__ == '__main__':
    unittest.main()

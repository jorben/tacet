import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from tacet_plugin_sdk.inference import BackendError, CPU, COREML, load_session


class InferencePolicyTest(unittest.TestCase):
    def runtime(self, available, behavior=None):
        calls = []
        def create(graph, sess_options, providers):
            provider = providers[0][0] if isinstance(providers[0], tuple) else providers[0]
            calls.append((graph, provider))
            actual = behavior(provider) if behavior else provider
            return SimpleNamespace(get_providers=lambda: [actual])
        return SimpleNamespace(get_available_providers=lambda: available, SessionOptions=SimpleNamespace,
                               InferenceSession=create, __version__='test',
                               ExecutionMode=SimpleNamespace(ORT_SEQUENTIAL=0)), calls

    def test_gpu_first_and_forced_cpu_use_the_same_graph(self):
        ort, calls = self.runtime([COREML, CPU])
        with tempfile.TemporaryDirectory() as tmp, patch.dict(sys.modules, onnxruntime=ort), patch('sys.platform', 'darwin'):
            _, backend = load_session(Path('/fixed/model_fp16.onnx'), 2, 'auto', Path(tmp))
            self.assertEqual(backend, COREML)
            _, backend = load_session(Path('/fixed/model_fp16.onnx'), 2, CPU, Path(tmp))
            self.assertEqual(backend, CPU)
        self.assertEqual(calls, [('/fixed/model_fp16.onnx', COREML), ('/fixed/model_fp16.onnx', CPU)])

    def test_no_gpu_uses_cpu_on_same_model(self):
        ort, calls = self.runtime([CPU])
        with patch.dict(sys.modules, onnxruntime=ort):
            _, actual = load_session(Path('/fixed/model_fp16.onnx'), 2, 'auto')
        self.assertEqual(actual, CPU)
        self.assertEqual(calls, [('/fixed/model_fp16.onnx', CPU)])

    def test_gpu_initialization_failure_falls_back_without_changing_graph(self):
        def fail_gpu(p):
            if p == COREML:
                raise RuntimeError('unsupported device or graph')
            return p
        ort, calls = self.runtime([COREML, CPU], fail_gpu)
        with tempfile.TemporaryDirectory() as tmp, patch.dict(sys.modules, onnxruntime=ort), patch('sys.platform', 'darwin'):
            _, actual = load_session(Path('/fixed/model_fp16.onnx'), 2, 'auto', Path(tmp))
        self.assertEqual(actual, CPU)
        self.assertEqual([p for _, p in calls], [COREML, CPU])
        self.assertEqual(len({g for g, _ in calls}), 1)

    def test_silent_provider_drop_reports_cpu_and_explicit_gpu_stays_strict(self):
        ort, calls = self.runtime([COREML, CPU], lambda _: CPU)
        with tempfile.TemporaryDirectory() as tmp, patch.dict(sys.modules, onnxruntime=ort), patch('sys.platform', 'darwin'):
            _, actual = load_session(Path('/fixed/model.onnx'), 2, 'auto', Path(tmp))
            self.assertEqual(actual, CPU)
            with self.assertRaises(BackendError):
                load_session(Path('/fixed/model.onnx'), 2, COREML, Path(tmp))

    def test_registered_gpu_with_zero_assigned_nodes_falls_back(self):
        ort, calls = self.runtime([COREML, CPU])
        create = ort.InferenceSession
        def unassigned(*args, **kwargs):
            session = create(*args, **kwargs)
            session.get_provider_graph_assignment_info = lambda: [SimpleNamespace(ep_name=CPU)]
            return session
        ort.InferenceSession = unassigned
        with tempfile.TemporaryDirectory() as tmp, patch.dict(sys.modules, onnxruntime=ort), patch('sys.platform', 'darwin'):
            _, actual = load_session(Path('/fixed/model.onnx'), 2, 'auto', Path(tmp))
        self.assertEqual(actual, CPU)
        self.assertEqual([p for _, p in calls], [COREML, CPU])

    def test_windows_prefers_directml_and_allows_cuda_then_cpu(self):
        ort, calls = self.runtime(['DmlExecutionProvider', 'CUDAExecutionProvider', CPU])
        with patch.dict(sys.modules, onnxruntime=ort), patch('sys.platform', 'win32'):
            _, actual = load_session(Path('/fixed/model.onnx'), 2, 'auto')
        self.assertEqual(actual, 'DmlExecutionProvider')
        self.assertEqual(calls[0][1], 'DmlExecutionProvider')

    def test_cpu_failure_is_propagated(self):
        def fail(_):
            raise RuntimeError('broken model')
        ort, _ = self.runtime([CPU], fail)
        with patch.dict(sys.modules, onnxruntime=ort), self.assertRaisesRegex(RuntimeError, 'broken model'):
            load_session(Path('/fixed/model.onnx'), 2, 'auto')

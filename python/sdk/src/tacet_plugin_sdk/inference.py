"""GPU-first ONNX sessions; CPU fallback always loads the very same graph and weights."""
from __future__ import annotations

import os
import sys
from pathlib import Path

CPU = 'CPUExecutionProvider'
COREML = 'CoreMLExecutionProvider'
GPU_PROVIDERS = (COREML, 'DmlExecutionProvider', 'CUDAExecutionProvider', 'ROCMExecutionProvider')


class BackendError(RuntimeError):
    pass


def prepare_cache(cache_dir: Path, version: str) -> Path:
    cache_dir = Path(cache_dir)
    if cache_dir.is_symlink() or (cache_dir.exists() and not cache_dir.is_dir()):
        raise BackendError('MODEL_CACHE_INVALID')
    path = cache_dir / ('ort-' + version)
    if path.is_symlink():
        raise BackendError('MODEL_CACHE_INVALID')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path, 0o700)
    return path


def providers_for(backend: str, cache_dir: Path | None) -> list:
    if backend != COREML:
        return [backend] if backend == CPU else [backend, CPU]
    if cache_dir is None:
        raise BackendError('MODEL_CACHE_REQUIRED')
    return [(COREML, {'MLComputeUnits': 'CPUAndGPU', 'ModelFormat': 'MLProgram',
                     'ModelCacheDirectory': str(cache_dir)}), CPU]


def load_session(graph: Path, threads: int, backend: str, cache_dir: Path | None = None):
    """auto tries available platform GPU providers, then CPU. Explicit providers stay strict.

    Only session setup is retried: integrity/tokenization/decoding errors are never masked as a GPU failure.
    """
    import onnxruntime as ort
    available = ort.get_available_providers()
    options = ort.SessionOptions()
    options.intra_op_num_threads = threads
    options.inter_op_num_threads = 1
    options.log_severity_level = 4
    if hasattr(ort.InferenceSession, 'get_provider_graph_assignment_info'):
        options.add_session_config_entry('session.record_ep_graph_assignment_info', '1')
    automatic = backend == 'auto'
    if automatic:
        preferred = (COREML,) if sys.platform == 'darwin' else (('DmlExecutionProvider', 'CUDAExecutionProvider')
                    if sys.platform == 'win32' else ('CUDAExecutionProvider', 'ROCMExecutionProvider'))
        candidates = [p for p in preferred if p in available] + [CPU]
    else:
        candidates = [backend]
    for candidate in candidates:
        if candidate not in available:
            if automatic:
                continue
            raise BackendError('MODEL_BACKEND_UNAVAILABLE')
        try:
            cache = prepare_cache(cache_dir, ort.__version__) if candidate == COREML and cache_dir is not None else cache_dir
            if candidate == 'DmlExecutionProvider':
                options.enable_mem_pattern = False
                options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
            session = ort.InferenceSession(str(graph), sess_options=options, providers=providers_for(candidate, cache))
            actual = session.get_providers()[0]
            # A registered GPU provider can receive zero nodes (e.g. unsupported operators).
            # Report GPU only after the graph assignment confirms it actually owns work.
            if candidate != CPU and hasattr(session, 'get_provider_graph_assignment_info'):
                assignments = session.get_provider_graph_assignment_info()
                if not any(group.ep_name == candidate for group in assignments):
                    actual = CPU
            if actual != candidate:
                del session
                raise BackendError('MODEL_BACKEND_UNAVAILABLE')
            return session, actual
        except Exception:
            if not automatic or candidate == CPU:
                raise
    raise BackendError('MODEL_BACKEND_UNAVAILABLE')

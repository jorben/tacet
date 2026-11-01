"""ONNX Runtime detector for the fixed AstrLink Guard candidate.

The manifest names the execution provider: the INT8 baseline runs on the CPU, the optional FP16
variant on Apple Silicon through Core ML (GPU). The provider in effect is checked after loading,
because ONNX Runtime silently drops a provider it does not have.
"""
from __future__ import annotations

import os
from pathlib import Path

from .decode import Span, decode_window, merge
from .integrity import IntegrityError, verify

WINDOW = 512
OVERLAP = 128
FLOOR = 0.5  # adapter floor, below the upstream recommended 0.8; policy applies the recipe value
COREML = 'CoreMLExecutionProvider'


class BackendError(RuntimeError):
    pass


def graph_of(manifest: dict) -> str:
    """The one ONNX graph of the manifest's files (external weight data is listed beside it)."""
    graphs = [name for name in manifest['files'] if name.endswith('.onnx')]
    if len(graphs) != 1:
        raise IntegrityError('MODEL_MANIFEST_INVALID')
    return graphs[0]


def providers_for(backend: str, cache_dir: Path | None) -> list:
    if backend != COREML:
        return [backend]
    if cache_dir is None:
        raise BackendError('MODEL_CACHE_REQUIRED')
    # CPUAndGPU measured fastest for the static FP16 graph (docs/v1/backend/m0-decisions.md); the
    # compiled model is cached so only the first load compiles it.
    options = {'MLComputeUnits': 'CPUAndGPU', 'ModelFormat': 'MLProgram', 'ModelCacheDirectory': str(cache_dir)}
    return [(COREML, options), 'CPUExecutionProvider']


def prepare_cache(cache_dir: Path, version: str) -> Path:
    """The cache folder of this ONNX Runtime version, private to the user. The host keys the parent by
    the manifest digest, so a changed model never meets a stale compiled one."""
    cache_dir = Path(cache_dir)
    if cache_dir.is_symlink() or (cache_dir.exists() and not cache_dir.is_dir()):
        raise BackendError('MODEL_CACHE_INVALID')
    path = cache_dir / ('ort-' + version)
    if path.is_symlink():
        raise BackendError('MODEL_CACHE_INVALID')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(path, 0o700)
    return path


class Detector:
    def __init__(self, directory: Path, manifest: dict, threads: int = 2, cache_dir: Path | None = None):
        verify(directory, manifest)  # full verification before importing inference code
        graph = graph_of(manifest)
        backend = manifest['backend']
        import onnxruntime as ort
        import tokenizers
        if backend not in ort.get_available_providers():
            raise BackendError('MODEL_BACKEND_UNAVAILABLE')
        if backend == COREML and cache_dir is not None:
            cache_dir = prepare_cache(cache_dir, ort.__version__)
        self._tokenizer = tokenizers.Tokenizer.from_file(str(Path(directory) / 'tokenizer.json'))
        self._tokenizer.enable_truncation(max_length=WINDOW, stride=OVERLAP)
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        options.log_severity_level = 4
        self._session = ort.InferenceSession(str(Path(directory) / graph), sess_options=options,
                                             providers=providers_for(backend, cache_dir))
        if self._session.get_providers()[0] != backend:
            raise BackendError('MODEL_BACKEND_UNAVAILABLE')
        self.backend = backend
        # a graph exported with a fixed sequence length takes windows padded to it
        length = self._session.get_inputs()[0].shape[1]
        self._static = length if isinstance(length, int) else None
        if self._static is not None and self._static < WINDOW:
            raise IntegrityError('MODEL_MANIFEST_INVALID')
        self.revision = manifest['revision']

    def _feed(self, window):
        import numpy as np
        ids, mask, types = list(window.ids), list(window.attention_mask), list(window.type_ids)
        if self._static is not None:
            pad = self._static - len(ids)
            ids, mask, types = ids + [0] * pad, mask + [0] * pad, types + [0] * pad
        return {'input_ids': np.asarray([ids], dtype=np.int64),
                'attention_mask': np.asarray([mask], dtype=np.int64),
                'token_type_ids': np.asarray([types], dtype=np.int64)}

    def detect(self, text: str) -> list[Span]:
        encoded = self._tokenizer.encode(text)
        spans = []
        for window in [encoded, *encoded.overflowing]:
            logits = self._session.run(['logits'], self._feed(window))[0][0][:len(window.ids)]
            spans.extend(decode_window(len(text), window.offsets, logits.astype('float32').tolist(), FLOOR))
        return merge(spans)

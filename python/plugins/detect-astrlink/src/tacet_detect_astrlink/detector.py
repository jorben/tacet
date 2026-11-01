"""ONNX Runtime detector for the fixed AstrLink Guard candidate.

The host chooses GPU-first or CPU execution independently from the fixed model manifest.
Fallback changes the execution provider while retaining the same graph and weights.
"""
from __future__ import annotations

from pathlib import Path

from tacet_plugin_sdk.inference import BackendError, COREML, load_session, prepare_cache, providers_for

from .decode import Span, decode_window, merge
from .integrity import IntegrityError, verify

WINDOW = 512
OVERLAP = 128
FLOOR = 0.5  # adapter floor, below the upstream recommended 0.8; policy applies the recipe value
COREML = 'CoreMLExecutionProvider'


def graph_of(manifest: dict) -> str:
    """The one ONNX graph of the manifest's files (external weight data is listed beside it)."""
    graphs = [name for name in manifest['files'] if name.endswith('.onnx')]
    if len(graphs) != 1:
        raise IntegrityError('MODEL_MANIFEST_INVALID')
    return graphs[0]


class Detector:
    def __init__(self, directory: Path, manifest: dict, threads: int = 2, cache_dir: Path | None = None, backend: str | None = None):
        verify(directory, manifest)  # full verification before importing inference code
        graph = graph_of(manifest)
        backend = backend or manifest['backend']
        import tokenizers
        self._tokenizer = tokenizers.Tokenizer.from_file(str(Path(directory) / 'tokenizer.json'))
        self._tokenizer.enable_truncation(max_length=WINDOW, stride=OVERLAP)
        self._session, self.backend = load_session(Path(directory) / graph, threads, backend, cache_dir)
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

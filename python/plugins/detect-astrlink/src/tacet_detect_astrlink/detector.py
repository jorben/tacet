"""CPU ONNX Runtime detector for the fixed AstrLink Guard candidate."""
from __future__ import annotations

import json
from pathlib import Path

from .decode import Span, decode_window, merge
from .integrity import verify

WINDOW = 512
OVERLAP = 128
FLOOR = 0.5  # adapter floor, below the upstream recommended 0.8; policy applies the recipe value


class Detector:
    def __init__(self, directory: Path, manifest: dict, threads: int = 2):
        verify(directory, manifest)  # full verification before importing inference code
        import onnxruntime as ort
        import tokenizers
        self._tokenizer = tokenizers.Tokenizer.from_file(str(Path(directory) / 'tokenizer.json'))
        self._tokenizer.enable_truncation(max_length=WINDOW, stride=OVERLAP)
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        options.log_severity_level = 4
        self._session = ort.InferenceSession(str(Path(directory) / 'model_int8.onnx'),
                                             sess_options=options, providers=['CPUExecutionProvider'])
        self.revision = manifest['revision']

    def detect(self, text: str) -> list[Span]:
        import numpy as np
        encoded = self._tokenizer.encode(text)
        spans = []
        for window in [encoded, *encoded.overflowing]:
            ids = np.asarray([window.ids], dtype=np.int64)
            feed = {'input_ids': ids,
                    'attention_mask': np.asarray([window.attention_mask], dtype=np.int64),
                    'token_type_ids': np.asarray([window.type_ids], dtype=np.int64)}
            logits = self._session.run(['logits'], feed)[0][0]
            spans.extend(decode_window(len(text), window.offsets, logits.tolist(), FLOOR))
        return merge(spans)

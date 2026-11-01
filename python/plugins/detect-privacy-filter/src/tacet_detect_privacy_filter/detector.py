"""ONNX Runtime detector for OpenAI Privacy Filter (CPU, as its manifest names).

Text is cut into windows of 512 tokens overlapping by 128, the width of the model's attention band, so every
token sees its context; a token's log-probabilities are averaged over the windows that hold it, as the
reference's aggregation does, and one Viterbi pass decodes the whole text. The reference uses 4096-token
windows on the CPU; measured on this build those peak at 5-7 GiB, 512 at about 2.2 GiB with the same spans
(docs/v1/backend/m0-decisions.md).
"""
from __future__ import annotations

import json
from pathlib import Path

from tacet_plugin_sdk.modelfiles import IntegrityError, verify

from .decode import DecodeError, LabelSpace, Span, char_ranges, log_softmax, spans_in_text, tables, viterbi

WINDOW = 512
OVERLAP = 128
FLOOR = 0.2  # adapter floor: spans whose weakest token is below it are not reported


class BackendError(RuntimeError):
    pass


def graph_of(manifest: dict) -> str:
    graphs = [name for name in manifest['files'] if name.endswith('.onnx')]
    if len(graphs) != 1:
        raise IntegrityError('MODEL_MANIFEST_INVALID')
    return graphs[0]


def byte_decoder() -> dict[str, int]:
    """The inverse of the byte-level BPE alphabet (each byte shown as one printable character)."""
    keep = list(range(ord('!'), ord('~') + 1)) + list(range(ord('¡'), ord('¬') + 1)) + list(range(ord('®'), ord('ÿ') + 1))
    chars, extra = list(keep), 0
    for b in range(256):
        if b not in keep:
            keep.append(b)
            chars.append(256 + extra)
            extra += 1
    return {chr(c): b for b, c in zip(keep, chars)}


def calibration(directory: Path) -> dict[str, float]:
    path = Path(directory) / 'viterbi_calibration.json'
    if not path.exists():
        return {}
    try:
        biases = json.loads(path.read_text(encoding='utf-8'))['operating_points']['default']['biases']
        return {k: float(v) for k, v in biases.items()}
    except (KeyError, TypeError, ValueError):
        raise IntegrityError('MODEL_MANIFEST_INVALID') from None


class Detector:
    def __init__(self, directory: Path, manifest: dict, threads: int = 2, window: int = WINDOW, overlap: int = OVERLAP):
        if not 0 <= overlap < window:
            raise ValueError('window')
        verify(directory, manifest)  # full verification before importing inference code
        directory = Path(directory)
        graph = graph_of(manifest)
        backend = manifest['backend']
        import onnxruntime as ort
        import tokenizers
        if backend not in ort.get_available_providers():
            raise BackendError('MODEL_BACKEND_UNAVAILABLE')
        config = json.loads((directory / 'config.json').read_text(encoding='utf-8'))
        id2label = config['id2label']
        self._space = LabelSpace([id2label[str(i)] for i in range(len(id2label))])
        self._start, self._end, self._trans = tables(self._space, calibration(directory))
        self._tokenizer = tokenizers.Tokenizer.from_file(str(directory / 'tokenizer.json'))
        self._tokenizer.encode_special_tokens = True  # "<|endoftext|>" in a document is text, not a control token
        self._bytes = byte_decoder()
        self._window, self._overlap = window, overlap
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        options.log_severity_level = 4
        self._session = ort.InferenceSession(str(directory / graph), sess_options=options, providers=[backend])
        if self._session.get_providers()[0] != backend:
            raise BackendError('MODEL_BACKEND_UNAVAILABLE')
        self.backend = backend
        self.revision = manifest['revision']

    def _token_bytes(self, ids: list[int]) -> list[bytes]:
        out = []
        for i in ids:
            token = self._tokenizer.id_to_token(i)
            try:
                out.append(bytes(self._bytes[c] for c in token))
            except (KeyError, TypeError):
                raise DecodeError('MODEL_OFFSETS_INVALID') from None
        return out

    def logprobs(self, ids: list[int]):
        """Per-token log-probabilities, averaged (in probability) over the overlapping windows."""
        import numpy as np
        classes = len(self._space.names)
        total = np.full((len(ids), classes), -np.inf)
        count = np.zeros(len(ids))
        stride = self._window - self._overlap
        at = 0
        while at < len(ids):
            chunk = np.asarray([ids[at:at + self._window]], dtype=np.int64)
            logits = self._session.run(None, {'input_ids': chunk, 'attention_mask': np.ones_like(chunk)})[0][0]
            if logits.shape != (chunk.shape[1], classes):
                raise DecodeError('MODEL_OUTPUT_INVALID')
            span = slice(at, at + chunk.shape[1])
            total[span] = np.logaddexp(total[span], log_softmax(logits))
            count[span] += 1
            if at + self._window >= len(ids):
                break
            at += stride
        return total - np.log(count)[:, None] if len(ids) else np.zeros((0, classes))

    def detect(self, text: str, floor: float = FLOOR) -> list[Span]:
        if not text:
            return []
        ids = self._tokenizer.encode(text, add_special_tokens=False).ids
        starts, ends = char_ranges(self._token_bytes(ids), text)
        lp = self.logprobs(ids)
        path = viterbi(lp, self._start, self._end, self._trans)
        return spans_in_text(path, lp, self._space, starts, ends, text, floor)

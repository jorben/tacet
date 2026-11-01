"""BIOES decoding of OpenAI Privacy Filter output, ported from the reference implementation.

github.com/openai/privacy-filter @ f7f00ca7 (Apache-2.0): opf/_core/decoding.py (ViterbiCRFDecoder),
opf/_core/spans.py (labels_to_spans, token_char_ranges_for_text, trim_char_spans_whitespace,
discard_overlapping_spans_by_label) and opf/_core/runtime.py (_select_non_overlapping_spans). The
reference reports no score; Tacet adds one per span: the lowest probability, along the decoded path,
of the label each of its tokens was given. It is not calibrated.
"""
from __future__ import annotations

import math
from bisect import bisect_left, bisect_right
from dataclasses import dataclass

import numpy as np

BACKGROUND = 'O'
TAGS = ('B', 'I', 'E', 'S')
BIASES = ('transition_bias_background_stay', 'transition_bias_background_to_start',
          'transition_bias_inside_to_continue', 'transition_bias_inside_to_end',
          'transition_bias_end_to_background', 'transition_bias_end_to_start')


class DecodeError(ValueError):
    pass


@dataclass(frozen=True)
class Span:
    label: str
    start: int
    end: int
    score: float


class LabelSpace:
    """Token classes (O plus B/I/E/S of each label) and the span label each belongs to."""

    def __init__(self, id2label: list[str]):
        self.names = list(id2label)
        self.background = None
        self.tag: list[str | None] = []
        self.span: list[str | None] = []
        seen: dict[str, set[str]] = {}
        for i, name in enumerate(self.names):
            if name == BACKGROUND:
                self.background = i
                self.tag.append(None)
                self.span.append(None)
                continue
            tag, _, label = name.partition('-')
            if tag not in TAGS or not label:
                raise DecodeError('MODEL_LABELS_INVALID')
            self.tag.append(tag)
            self.span.append(label)
            seen.setdefault(label, set()).add(tag)
        if self.background is None or any(tags != set(TAGS) for tags in seen.values()):
            raise DecodeError('MODEL_LABELS_INVALID')
        self.labels = sorted(seen)


def tables(space: LabelSpace, biases: dict[str, float] | None = None):
    """Start, end and transition scores: -inf for a forbidden edge, else the edge's bias."""
    b = {k: float((biases or {}).get(k, 0.0)) for k in BIASES}
    n = len(space.names)
    start = np.full(n, -np.inf, dtype=np.float64)
    end = np.full(n, -np.inf, dtype=np.float64)
    trans = np.full((n, n), -np.inf, dtype=np.float64)
    bg = space.background
    for i in range(n):
        tag, label = space.tag[i], space.span[i]
        if i == bg or tag in ('B', 'S'):
            start[i] = 0.0
        if i == bg or tag in ('E', 'S'):
            end[i] = 0.0
        for j in range(n):
            ntag, nlabel = space.tag[j], space.span[j]
            nbg = j == bg
            if i == bg or tag in ('E', 'S'):
                if not (nbg or ntag in ('B', 'S')):
                    continue
                if i == bg:
                    trans[i, j] = b['transition_bias_background_stay'] if nbg else b['transition_bias_background_to_start']
                else:
                    trans[i, j] = b['transition_bias_end_to_background'] if nbg else b['transition_bias_end_to_start']
            elif tag in ('B', 'I'):
                if nbg or nlabel != label or ntag not in ('I', 'E'):
                    continue
                trans[i, j] = b['transition_bias_inside_to_continue'] if ntag == 'I' else b['transition_bias_inside_to_end']
    return start, end, trans


def viterbi(logprobs: np.ndarray, start: np.ndarray, end: np.ndarray, trans: np.ndarray) -> list[int]:
    """The best label path through `[T, C]` log-probabilities under the transition constraints."""
    if logprobs.ndim != 2 or logprobs.shape[1] != len(start):
        raise DecodeError('MODEL_OUTPUT_INVALID')
    t = logprobs.shape[0]
    if t == 0:
        return []
    if not np.isfinite(logprobs).all():
        raise DecodeError('MODEL_OUTPUT_INVALID')
    scores = logprobs[0] + start
    back = np.empty((t - 1, len(start)), dtype=np.int64)
    for i in range(1, t):
        cand = scores[:, None] + trans
        back[i - 1] = cand.argmax(axis=0)
        scores = cand.max(axis=0) + logprobs[i]
    if not np.isfinite(scores).any():
        return logprobs.argmax(axis=1).tolist()
    scores = scores + end
    last = int(scores.argmax())
    path = [last]
    for i in range(t - 2, -1, -1):
        last = int(back[i, last])
        path.append(last)
    return path[::-1]


def labels_to_spans(path: list[int], space: LabelSpace) -> list[tuple[str, int, int]]:
    """Token spans `(label, first, end)` of a decoded path (the reference's labels_to_spans)."""
    spans: list[tuple[str, int, int]] = []
    current, first = None, None
    for i, k in enumerate(path):
        label, tag = space.span[k], space.tag[k]
        if label is None:
            if current is not None:
                spans.append((current, first, i))
            current, first = None, None
        elif tag == 'S':
            if current is not None:
                spans.append((current, first, i))
            spans.append((label, i, i + 1))
            current, first = None, None
        elif tag == 'B':
            if current is not None:
                spans.append((current, first, i))
            current, first = label, i
        elif tag == 'I':
            if current != label:
                if current is not None:
                    spans.append((current, first, i))
                current, first = label, i
        else:  # E
            if current != label:
                if current is not None:
                    spans.append((current, first, i))
                spans.append((label, i, i + 1))
            else:
                spans.append((current, first, i + 1))
            current, first = None, None
    if current is not None:
        spans.append((current, first, len(path)))
    return spans


def char_ranges(token_bytes: list[bytes], text: str) -> tuple[list[int], list[int]]:
    """Code-point range of every token in `text`, from the UTF-8 bytes each token stands for. A token
    that holds part of a character covers that whole character."""
    if not text.encode('utf-8').startswith(b''.join(token_bytes)):
        raise DecodeError('MODEL_OFFSETS_INVALID')
    char_starts, char_ends, cursor = [], [], 0
    for ch in text:
        char_starts.append(cursor)
        cursor += len(ch.encode('utf-8'))
        char_ends.append(cursor)
    starts, ends, cursor = [], [], 0
    for raw in token_bytes:
        a, cursor = cursor, cursor + len(raw)
        s = bisect_right(char_ends, a)
        e = bisect_left(char_starts, cursor)
        starts.append(s)
        ends.append(max(e, s))
    return starts, ends


def spans_in_text(path: list[int], logprobs: np.ndarray, space: LabelSpace, starts: list[int], ends: list[int],
                  text: str, floor: float) -> list[Span]:
    """Character spans: converted, whitespace-trimmed, overlaps within a label dropped, then a
    left-to-right non-overlapping selection, as the reference does."""
    chars: list[tuple[str, int, int, float]] = []
    for label, a, b in labels_to_spans(path, space):
        start, end = starts[a], ends[b - 1]
        while start < end and text[start].isspace():
            start += 1
        while end > start and text[end - 1].isspace():
            end -= 1
        if end <= start:
            continue
        score = min(math.exp(float(logprobs[i, path[i]])) for i in range(a, b))
        chars.append((label, start, end, min(1.0, max(0.0, score))))
    by_label: dict[str, list[tuple[str, int, int, float]]] = {}
    for c in chars:
        by_label.setdefault(c[0], []).append(c)
    kept = []
    for group in by_label.values():
        mine: list[tuple[str, int, int, float]] = []
        for c in sorted(group, key=lambda c: (c[1], -(c[2] - c[1]))):
            if all(c[2] <= k[1] or c[1] >= k[2] for k in mine):
                mine.append(c)
        kept.extend(mine)
    out, cursor = [], 0
    for label, start, end, score in sorted(kept, key=lambda c: (c[1], -(c[2] - c[1]), c[0])):
        if start < cursor:
            continue
        cursor = end
        if score >= floor:
            out.append(Span(label, start, end, score))
    return out


def log_softmax(logits: np.ndarray) -> np.ndarray:
    x = logits.astype(np.float64)
    x = x - x.max(axis=-1, keepdims=True)
    return x - np.log(np.exp(x).sum(axis=-1, keepdims=True))

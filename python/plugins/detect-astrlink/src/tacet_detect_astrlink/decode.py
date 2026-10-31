"""Window decoding for AstrLink Guard token classification (architecture 8.2).

This follows the BIO decoding published in the upstream model card's reference
snippet (a pinned-revision differential test compares the two on real model
output). The upstream `bio-offset-consistency-v1` and
`existing-ip-and-dynamic-url-v1` contracts are only named, not specified, in the
model repository, so conformance to them is NOT claimed: the IP/URL boundary
cleanup is not implemented and no per-category evaluation exists yet. An earlier
version split spans whenever token offsets overlapped; that fragmented real
dates and addresses and was removed. Offsets are Unicode
code points of the unit text (tokenizers returns character offsets).
The adapter returns every span at or above the adapter floor with its raw
score; the recipe's minimum-confidence policy is applied later in `policy`.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

LABELS = ("O", "B-email", "I-email", "B-phone", "I-phone", "B-account", "I-account",
          "B-payment_card", "I-payment_card", "B-ip_address", "I-ip_address", "B-url", "I-url",
          "B-common_secret", "I-common_secret", "B-private_address", "I-private_address",
          "B-private_date", "I-private_date", "B-private_person", "I-private_person")


class DecodeError(ValueError):
    pass


@dataclass(frozen=True)
class Span:
    label: str
    start: int
    end: int
    score: float


def _softmax_max(row):
    peak = max(row)
    exps = [math.exp(v - peak) for v in row]
    total = sum(exps)
    best = max(range(len(row)), key=row.__getitem__)
    return best, exps[best] / total


def decode_window(text_length: int, offsets, logits, floor: float = 0.0) -> list[Span]:
    """Decode one window. `logits` is a sequence of 21-float rows per token."""
    if len(offsets) != len(logits):
        raise DecodeError('DECODE_SHAPE_MISMATCH')
    spans: list[Span] = []
    current = None
    for (start, end), row in zip(offsets, logits):
        if len(row) != len(LABELS) or not all(math.isfinite(v) for v in row):
            raise DecodeError('DECODE_OUTPUT_INVALID')
        if start == end:  # special tokens
            continue
        if not 0 <= start < end <= text_length:
            raise DecodeError('DECODE_OFFSET_INVALID')
        index, score = _softmax_max(row)
        tag, _, kind = LABELS[index].partition('-')
        if tag == 'O':
            current = None
        elif tag == 'B' or current is None or current['label'] != kind:
            current = {'label': kind, 'start': start, 'end': end, 'score': score}
            spans.append(current)
        else:
            # adjacent SentencePiece tokens can overlap by one character, so never shrink
            current['end'] = max(current['end'], end)
            current['score'] = min(current['score'], score)
    return [Span(s['label'], s['start'], s['end'], s['score']) for s in spans if s['score'] >= floor]


def merge(spans: list[Span]) -> list[Span]:
    """Union overlapping same-label spans from overlapping windows.

    The merged score is the maximum of the contributing spans: a boundary token
    is least reliable at a window edge, so the better-supported observation
    wins instead of an arbitrary window order.
    """
    result: list[Span] = []
    for span in sorted(spans, key=lambda s: (s.label, s.start, s.end)):
        if result and result[-1].label == span.label and span.start <= result[-1].end:
            last = result[-1]
            result[-1] = Span(last.label, last.start, max(last.end, span.end), max(last.score, span.score))
        else:
            result.append(span)
    return sorted(result, key=lambda s: (s.start, s.end, s.label))

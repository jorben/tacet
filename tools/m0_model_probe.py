#!/usr/bin/env python3
"""M0 CPU loading/window/integrity probe; does not claim decoder conformance.

The official offset-consistent BIO and IP/URL cleanup contracts must be
implemented and independently evaluated before this model is a real detector.
No source text, token IDs, logits or raw candidate values are written to reports.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import platform
import resource
import sys
import time
from pathlib import Path

from tacet_detect_astrlink.integrity import verify

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    manifest_path = ROOT / 'resources/models/astrlink-guard-int8.json'
    manifest = json.loads(manifest_path.read_text())
    start = time.monotonic()
    verify(args.directory, manifest)
    verified = time.monotonic()
    # Import inference libraries only after full file verification.
    import numpy as np
    import onnxruntime as ort
    import tokenizers
    tokenizer = tokenizers.Tokenizer.from_file(str(args.directory / 'tokenizer.json'))
    tokenizer.enable_truncation(max_length=512, stride=128)
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    options.log_severity_level = 4
    session = ort.InferenceSession(str(args.directory / 'model_int8.onnx'),
                                  sess_options=options, providers=['CPUExecutionProvider'])
    loaded = time.monotonic()
    texts = [
        '请联系张伟，电话 13812345678，邮箱 zhangwei@example.com。',
        'Contact Jane Doe at jane.doe@example.org. Employee number: TEST-007.',
        '公开示例 document ' * 1000 + '联系人张伟，电话 13812345678。',
    ]
    results = []
    for text in texts:
        encoded = tokenizer.encode(text)
        windows = [encoded, *encoded.overflowing]
        elapsed, tokens, labeled = 0.0, 0, 0
        coverage = []
        for window in windows:
            if len(window.ids) > 512:
                raise RuntimeError('M0_MODEL_WINDOW_LIMIT')
            ids = np.asarray([window.ids], dtype=np.int64)
            feed = {'input_ids': ids,
                    'attention_mask': np.asarray([window.attention_mask], dtype=np.int64),
                    'token_type_ids': np.asarray([window.type_ids], dtype=np.int64)}
            before = time.monotonic()
            logits = session.run(['logits'], feed)[0]
            elapsed += time.monotonic() - before
            if logits.shape != (1, len(window.ids), 21) or not np.isfinite(logits).all():
                raise RuntimeError('M0_MODEL_OUTPUT_INVALID')
            tokens += len(window.ids)
            labeled += int((logits.argmax(axis=-1) != 0).sum())
            for begin, end in window.offsets:
                if begin < 0 or end < begin or end > len(text):
                    raise RuntimeError('M0_MODEL_OFFSET_INVALID')
                if begin < end:
                    coverage.append((begin, end))
        # Verify the final long-input unit wasn't silently truncated.
        if not coverage or max(end for _, end in coverage) != len(text):
            raise RuntimeError('M0_MODEL_INPUT_TRUNCATED')
        results.append({'characters': len(text), 'windows': len(windows),
                        'tokens_with_overlap': tokens, 'non_O_token_predictions': labeled,
                        'inference_seconds': elapsed})
    report = {
        'probe': 'm0-astrlink-loading-and-windowing', 'status': 'passed',
        'model_revision': manifest['revision'], 'variant': manifest['variant'],
        'manifest_sha256': hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
        'runtime': {'python': platform.python_version(), 'onnxruntime': ort.__version__,
                    'tokenizers': tokenizers.__version__, 'numpy': np.__version__,
                    'providers': session.get_providers(), 'intra_threads': 2, 'inter_threads': 1},
        'seconds': {'integrity': verified-start, 'load': loaded-verified},
        'inputs': results,
        'peak_rss_bytes': resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if sys.platform == 'darwin' else 1024),
        'limitations': ['synthetic smoke inputs only', 'not the official BIO decoder/span cleanup',
                        'no precision/recall, calibration or unattended qualification',
                        'not sandboxed or managed release deployment'],
    }
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))


if __name__ == '__main__':
    main()

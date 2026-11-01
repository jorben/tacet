"""Protocol entry of the AstrLink Guard detection plugin (role: detect).

Launch: python -I -m tacet_detect_astrlink --model-dir DIR --manifest FILE [--cache-dir DIR]
The host passes the paths (the cache folder only for a backend that compiles the model); they are launch arguments, never protocol data. The
model is verified against its manifest before any inference library is
imported, and loaded once per worker.
"""
import argparse
import json
import sys
from pathlib import Path

from tacet_plugin_sdk.runtime import Worker, TaskError
from tacet_plugin_sdk.schema import Schemas, SchemaError

BATCH = 128
_detector = None
_schemas = Schemas()
for _definition in ('Params', 'Item'):
    _schemas.compile('detect.schema.json', _definition)


def detect_run(task, params):
    try:
        _schemas.validate('detect.schema.json', 'Params', params)
    except SchemaError:
        raise TaskError('PROTOCOL_INVALID') from None
    spans = _detector.detect(params['text'])
    total = 0
    for offset in range(0, len(spans), BATCH):
        task.check()
        batch = [{'label': s.label, 'start': s.start, 'end': s.end, 'score': float(s.score)}
                 for s in spans[offset:offset + BATCH]]
        item = {'kind': 'spans', 'spans': batch}
        _schemas.validate('detect.schema.json', 'Item', item)
        task.emit(item)
        total += len(batch)
    task.emit({'kind': 'unit_done', 'spans': total})


def main(argv):
    global _detector
    parser = argparse.ArgumentParser(prog='tacet_detect_astrlink')
    parser.add_argument('--model-dir', required=True)
    parser.add_argument('--manifest', required=True)
    parser.add_argument('--cache-dir')
    parser.add_argument('--backend', default='auto')
    args = parser.parse_args(argv)
    from .detector import Detector
    manifest = json.loads(Path(args.manifest).read_text(encoding='utf-8'))
    _detector = Detector(Path(args.model_dir), manifest, cache_dir=Path(args.cache_dir) if args.cache_dir else None, backend=args.backend)
    return Worker(sys.stdin.buffer, sys.stdout.buffer, 'tacet.astrlink', '0.1.0',
                  ['detect'], {'detect.run': detect_run}, capabilities={'backend.' + _detector.backend: True}).run()


if __name__ == '__main__':
    raise SystemExit(main(sys.argv[1:]))

"""Reading the operation plan resource: JSON Lines, one operation per line.

The plan is written by the host from the disposition result; the plugin only
executes it. Every line is validated against ``PlanLine`` before use, so a
malformed or oversized plan fails the call instead of being half applied.
"""
from __future__ import annotations

import json
from dataclasses import dataclass

from tacet_plugin_sdk.runtime import TaskError

MAX_PLAN_BYTES = 64 << 20
MAX_OPERATIONS = 400_000


@dataclass(frozen=True)
class Op:
    id: str
    kind: str  # text | part
    part: str
    ordinal: int = 0
    start: int = 0
    end: int = 0
    replacement: str = ''


def read_plan(source, schemas) -> list[Op]:
    chunks, total = [], 0
    while True:
        block = source.read(1 << 20)
        if not block:
            break
        total += len(block)
        if total > MAX_PLAN_BYTES:
            raise TaskError('RESOURCE_LIMIT')
        chunks.append(block)
    data = b''.join(chunks)
    ops, seen = [], set()
    if not data:
        return ops
    try:
        text = data.decode('utf-8')
    except UnicodeDecodeError:
        raise TaskError('PROTOCOL_INVALID') from None
    for line in text.split('\n'):
        if not line:
            continue
        if len(ops) >= MAX_OPERATIONS:
            raise TaskError('RESOURCE_LIMIT')
        try:
            item = json.loads(line)
            schemas.validate('format.schema.json', 'PlanLine', item)
        except Exception:
            raise TaskError('PROTOCOL_INVALID') from None
        if item['id'] in seen or (item['kind'] == 'text' and item['end'] <= item['start']):
            raise TaskError('PROTOCOL_INVALID')
        seen.add(item['id'])
        ops.append(Op(item['id'], item['kind'], item['part'], item.get('ordinal', 0), item.get('start', 0), item.get('end', 0), item.get('replacement', '')))
    return ops

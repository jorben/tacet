"""Bounded one-heavy-task JSON-RPC worker with an independent control reader.

Handlers receive values/capabilities, never a scheduler or direct publish path.
The reader remains available while a task waits for byte/item credit. Cooperative
cancellation has a bounded wait; the Go supervisor must reclaim unresponsive
process trees. No Python exception text crosses the process boundary.
"""
from __future__ import annotations

import json
import threading
import time
from dataclasses import dataclass
from typing import BinaryIO, Callable

from . import framing
from .schema import Schemas, SchemaError


class Cancelled(Exception):
    pass


class TaskError(Exception):
    def __init__(self, code='PLUGIN_TASK_FAILED'):
        super().__init__(code)
        # Do not accept arbitrary handler text as protocol diagnostics.
        self.code = code if code in {'PLUGIN_TASK_FAILED', 'RESOURCE_LIMIT', 'PROTOCOL_INVALID',
                                     'DEADLINE_EXCEEDED', 'CANCELLED',
                                     'PASSWORD_INCORRECT', 'ENCRYPTION_UNSUPPORTED', 'NOT_ENCRYPTED', 'INPUT_CORRUPT',
                                     'FORMAT_UNSUPPORTED', 'OUTPUT_LIMIT'} else 'PLUGIN_TASK_FAILED'


@dataclass(frozen=True)
class TaskRef:
    job_id: str
    attempt_id: str
    owner_generation: int
    job_version: int


class Task:
    def __init__(self, ref: TaskRef, stream: str, byte_credit: int, item_credit: int,
                 deadline_ms: int, send: Callable[[dict], None]):
        if not 0 < byte_credit <= framing.MAX_MESSAGE_BYTES or not 0 < item_credit <= 65536:
            raise TaskError('PROTOCOL_INVALID')
        if type(deadline_ms) is not int or not 0 < deadline_ms <= 24 * 60 * 60 * 1000:
            raise TaskError('PROTOCOL_INVALID')
        self.ref = ref
        self.stream = stream
        self._bytes = byte_credit
        self._items = item_credit
        self._max_bytes = byte_credit
        self._max_items = item_credit
        self._send = send
        self._condition = threading.Condition()
        self._cancelled = False
        self._deadline = time.monotonic() + deadline_ms / 1000
        self.sequence = 0
        self.committed = 0
        self._pending = {}
        self._available_bytes = 0
        self._available_items = 0

    def check(self):
        if self._cancelled:
            raise Cancelled()
        if time.monotonic() >= self._deadline:
            raise TaskError('DEADLINE_EXCEEDED')

    def cancel(self):
        with self._condition:
            self._cancelled = True
            self._condition.notify_all()

    def emit(self, item):
        # Charge the serialized item, not just its text, identically to host.
        body = json.dumps(item, ensure_ascii=False, separators=(',', ':'), allow_nan=False).encode()
        size = len(body)
        if not 0 < size <= self._max_bytes:
            raise TaskError('RESOURCE_LIMIT')
        with self._condition:
            while self._bytes < size or self._items < 1:
                self.check()
                self._condition.wait(timeout=min(0.1, max(0, self._deadline-time.monotonic())))
            self.check()
            self._bytes -= size
            self._items -= 1
            self.sequence += 1
            self._pending[self.sequence] = size
            self._send({'jsonrpc': '2.0', 'method': 'stream.item', 'params': {
                'stream': self.stream, 'seq': self.sequence, 'item': item}})

    def credit(self, sequence, byte_credit, item_credit):
        with self._condition:
            if (type(sequence) is not int or type(byte_credit) is not int or type(item_credit) is not int
                    or not self.committed <= sequence <= self.sequence
                    or byte_credit < 0 or item_credit < 0):
                raise TaskError('PROTOCOL_INVALID')
            for seq in range(self.committed+1, sequence+1):
                self._available_bytes += self._pending.pop(seq)
                self._available_items += 1
            self.committed = sequence
            if (byte_credit > self._available_bytes or item_credit > self._available_items
                    or byte_credit > self._max_bytes-self._bytes
                    or item_credit > self._max_items-self._items):
                raise TaskError('PROTOCOL_INVALID')
            self._available_bytes -= byte_credit
            self._available_items -= item_credit
            self._bytes += byte_credit
            self._items += item_credit
            self._condition.notify_all()

    def await_commit(self):
        with self._condition:
            while self.committed != self.sequence:
                self.check()
                self._condition.wait(timeout=0.1)
            self.check()


class Worker:
    def __init__(self, source: BinaryIO, destination: BinaryIO, plugin_id: str,
                 version: str, roles: list[str], handlers: dict[str, Callable]):
        self.source, self.destination = source, destination
        self.plugin_id, self.version, self.roles = plugin_id, version, roles
        self.handlers = handlers
        self.schemas = Schemas()
        for name, definition in [('rpc.schema.json', 'Message'), ('initialize.schema.json', 'Params'),
                                 ('initialize.schema.json', 'Result'), ('stream.schema.json', 'Credit'),
                                 ('lifecycle.schema.json', 'Task'), ('lifecycle.schema.json', 'Cancel'),
                                 ('lifecycle.schema.json', 'Empty')]:
            self.schemas.compile(name, definition)
        self._send_lock = threading.Lock()
        self._task_lock = threading.Lock()
        self._task = None
        self._thread = None
        self._generation = None
        self._initialized = False

    def _send(self, message):
        self.schemas.validate('rpc.schema.json', 'Message', message)
        frame = framing.encode(message)
        with self._send_lock:
            self.destination.write(frame)
            self.destination.flush()

    def _failure(self, identifier, code):
        self._send({'jsonrpc': '2.0', 'id': identifier, 'error': {
            'code': -32000, 'message': 'Plugin request failed', 'data': {'code': code, 'retry': 'none'}}})

    def _execute(self, identifier, method, params, task):
        try:
            self.handlers[method](task, params)
            task.await_commit()
            result = {'type': 'stream.end', 'stream': task.stream,
                      'last_seq': task.sequence, 'status': 'completed'}
        except Cancelled:
            result = {'type': 'stream.end', 'stream': task.stream,
                      'last_seq': task.sequence, 'status': 'cancelled'}
        except TaskError as error:
            result = {'type': 'stream.end', 'stream': task.stream,
                      'last_seq': task.sequence, 'status': 'failed',
                      'error': {'code': error.code, 'retry': 'none'}}
        except Exception:
            result = {'type': 'stream.end', 'stream': task.stream,
                      'last_seq': task.sequence, 'status': 'failed',
                      'error': {'code': 'PLUGIN_TASK_FAILED', 'retry': 'none'}}
        # Release the heavy-task slot before publishing its final response;
        # otherwise a caller receiving that response can race the cleanup and
        # incorrectly see WORKER_BUSY on its next request.
        with self._task_lock:
            if self._task is task:
                self._task = None
        self._send({'jsonrpc': '2.0', 'id': identifier, 'result': result})

    def _dispatch(self, message):
        self.schemas.validate('rpc.schema.json', 'Message', message)
        method, params = message.get('method'), message.get('params', {})
        identifier = message.get('id')
        if method == 'initialize':
            if self._initialized:
                raise TaskError('PROTOCOL_INVALID')
            self.schemas.validate('initialize.schema.json', 'Params', params)
            if params['protocol'] != {'min': '1.0', 'max': '1.0'}:
                self._failure(identifier, 'PROTOCOL_UNSUPPORTED')
                return
            self._generation = params['owner_generation']
            result = {'protocol': '1.0', 'plugin': {'id': self.plugin_id, 'version': self.version},
                      'roles': self.roles, 'capabilities': {}}
            self.schemas.validate('initialize.schema.json', 'Result', result)
            self._send({'jsonrpc': '2.0', 'id': identifier, 'result': result})
            self._initialized = True
            return
        if not self._initialized:
            raise TaskError('PROTOCOL_INVALID')
        if method == 'health':
            self.schemas.validate('lifecycle.schema.json', 'Empty', params)
            if identifier is None:
                raise TaskError('PROTOCOL_INVALID')
            self._send({'jsonrpc': '2.0', 'id': identifier, 'result': {'status': 'ready'}})
            return
        if method in {'stream.credit', 'task.cancel'}:
            with self._task_lock:
                task = self._task
                if task is None:
                    raise TaskError('PROTOCOL_INVALID')
                if method == 'stream.credit':
                    self.schemas.validate('stream.schema.json', 'Credit', params)
                    if params['stream'] != task.stream:
                        raise TaskError('PROTOCOL_INVALID')
                    task.credit(params['committed_seq'], params.get('bytes', 0), params.get('items', 0))
                else:
                    self.schemas.validate('lifecycle.schema.json', 'Cancel', params)
                    if (params.get('job_id'), params.get('attempt_id'), params.get('owner_generation'), params.get('job_version')) != (
                            task.ref.job_id, task.ref.attempt_id, task.ref.owner_generation, task.ref.job_version):
                        raise TaskError('PROTOCOL_INVALID')
                    task.cancel()
            return
        if method not in self.handlers or identifier is None:
            if identifier is not None:
                self._failure(identifier, 'METHOD_UNSUPPORTED')
                return
            raise TaskError('PROTOCOL_INVALID')
        self.schemas.validate('lifecycle.schema.json', 'Task', params)
        with self._task_lock:
            if self._task is not None:
                self._failure(identifier, 'WORKER_BUSY')
                return
            ref = TaskRef(params.get('job_id'), params.get('attempt_id'),
                          params.get('owner_generation'), params.get('job_version'))
            if (not isinstance(ref.job_id, str) or not ref.job_id or len(ref.job_id) > 128
                    or not isinstance(ref.attempt_id, str) or not ref.attempt_id or len(ref.attempt_id) > 128
                    or type(ref.owner_generation) is not int or ref.owner_generation != self._generation
                    or type(ref.job_version) is not int or ref.job_version < 1 or params.get('protocol') != '1.0'):
                raise TaskError('PROTOCOL_INVALID')
            stream = params.get('stream', {})
            credit = stream.get('credit', {})
            if not isinstance(stream.get('id'), str) or not 0 < len(stream['id']) <= 128:
                raise TaskError('PROTOCOL_INVALID')
            task = Task(ref, stream['id'], credit.get('bytes', 0), credit.get('items', 0), params.get('deadline_ms', 0), self._send)
            self._task = task
            self._thread = threading.Thread(target=self._execute, args=(identifier, method, params, task), daemon=True)
            self._thread.start()

    def run(self):
        try:
            while (message := framing.read(self.source)) is not None:
                self._dispatch(message)
        except (framing.FrameError, SchemaError, TaskError, OSError, ValueError, TypeError, KeyError):
            return 1
        finally:
            with self._task_lock:
                task = self._task
                if task is not None:
                    task.cancel()
            if self._thread is not None:
                self._thread.join(timeout=1)
        return 0

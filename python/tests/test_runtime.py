import json
import queue
import subprocess
import sys
import threading
import unittest
from pathlib import Path

from tacet_plugin_sdk import framing


class RuntimeTest(unittest.TestCase):
    def setUp(self):
        self.process = None
        self.addCleanup(self.tearDown_process)
        self.setUp_process()

    def setUp_process(self, initialize=True):
        self.process = subprocess.Popen([sys.executable, '-I', str(Path(__file__).with_name('worker_fixture.py'))],
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.messages = queue.Queue()
        process = self.process

        def reader():
            try:
                while (message := framing.read(process.stdout)) is not None:
                    self.messages.put(message)
            except Exception as error:
                self.messages.put(error)
        threading.Thread(target=reader, daemon=True).start()
        if initialize:
            self.send({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {
                'protocol': {'min': '1.0', 'max': '1.0'}, 'owner_generation': 7,
                'limits': {'max_message_bytes': framing.MAX_MESSAGE_BYTES}}})
            self.assertEqual(self.receive()['result']['protocol'], '1.0')

    def tearDown_process(self):
        process, self.process = self.process, None
        if process is None:
            return
        if process.poll() is None:
            process.terminate()
        process.wait(timeout=5)
        for stream in (process.stdin, process.stdout, process.stderr):
            try:
                stream.close()
            except OSError:
                pass  # a broken pipe on an already-dead worker

    def send(self, message):
        self.process.stdin.write(framing.encode(message))
        self.process.stdin.flush()

    def receive(self):
        value = self.messages.get(timeout=5)
        if isinstance(value, Exception):
            raise value
        return value

    def start(self, method='fixture.stream', generation=7, deadline_ms=10000, identifier=2):
        self.send({'jsonrpc': '2.0', 'id': identifier, 'method': method, 'params': {
            'protocol': '1.0', 'job_id': 'j', 'attempt_id': 'a', 'owner_generation': generation,
            'job_version': 1, 'deadline_ms': deadline_ms,
            'stream': {'id': 's', 'credit': {'bytes': 100, 'items': 1}}}})

    def credit(self, message):
        params = message['params']
        size = len(json.dumps(params['item'], ensure_ascii=False, separators=(',', ':')).encode())
        self.send({'jsonrpc': '2.0', 'method': 'stream.credit', 'params': {
            'stream': 's', 'committed_seq': params['seq'], 'bytes': size, 'items': 1}})

    def test_stream_completes_only_after_durable_ack_and_capacity_release(self):
        self.start()
        for index in range(3):
            item = self.receive()
            self.assertEqual(item['params']['seq'], index+1)
            self.assertEqual(item['params']['item']['index'], index)
            self.credit(item)
        end = self.receive()
        self.assertEqual(end['result']['status'], 'completed')
        self.assertEqual(end['result']['last_seq'], 3)

    def test_control_health_and_cancel_work_while_data_is_backpressured(self):
        self.start()
        first = self.receive()
        self.assertEqual(first['params']['seq'], 1)
        # No credit is given: the heavy handler cannot emit a second item.
        self.send({'jsonrpc': '2.0', 'id': 3, 'method': 'health', 'params': {}})
        self.assertEqual(self.receive()['result']['status'], 'ready')
        self.send({'jsonrpc': '2.0', 'method': 'task.cancel', 'params': {
            'job_id': 'j', 'attempt_id': 'a', 'owner_generation': 7, 'job_version': 1}})
        result = self.receive()['result']
        self.assertEqual(result['status'], 'cancelled')
        self.assertEqual(result['last_seq'], 1)

    def test_handler_exception_is_redacted(self):
        self.start('fixture.explode')
        message = self.receive()
        self.assertEqual(message['result']['error']['code'], 'PLUGIN_TASK_FAILED')
        self.assertNotIn('TEST_SECRET', json.dumps(message))

    def test_old_generation_request_exits_without_executing_handler(self):
        self.start(generation=6)
        self.assertEqual(self.process.wait(timeout=5), 1)


    def cancel(self, **override):
        params = {'job_id': 'j', 'attempt_id': 'a', 'owner_generation': 7, 'job_version': 1}
        params.update(override)
        self.send({'jsonrpc': '2.0', 'method': 'task.cancel', 'params': params})

    def test_cooperative_task_stops_on_cancel_with_a_cancelled_end(self):
        self.start('fixture.cooperative')
        self.send({'jsonrpc': '2.0', 'id': 3, 'method': 'health', 'params': {}})
        self.assertEqual(self.receive()['result']['status'], 'ready')  # control lane works while the task runs
        self.cancel()
        result = self.receive()['result']
        self.assertEqual((result['status'], result['last_seq']), ('cancelled', 0))
        self.start('fixture.stream', identifier=4)  # the worker slot is free again
        self.assertEqual(self.receive()['params']['seq'], 1)

    def test_deadline_is_enforced_inside_the_plugin(self):
        self.start('fixture.cooperative', deadline_ms=150)
        result = self.receive()['result']
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(result['error']['code'], 'DEADLINE_EXCEEDED')

    def test_item_larger_than_the_byte_credit_is_refused_by_the_sdk(self):
        self.start('fixture.oversized')  # credit is 100 bytes
        result = self.receive()['result']
        self.assertEqual((result['status'], result['last_seq']), ('failed', 0))
        self.assertEqual(result['error']['code'], 'RESOURCE_LIMIT')

    def test_second_heavy_task_is_refused_while_one_runs(self):
        self.start('fixture.cooperative')
        self.start('fixture.cooperative', identifier=3)
        busy = self.receive()
        self.assertEqual(busy['id'], 3)
        self.assertEqual(busy['error']['data']['code'], 'WORKER_BUSY')
        self.cancel()
        self.assertEqual(self.receive()['result']['status'], 'cancelled')

    def test_unknown_method_is_an_error_response_not_a_crash(self):
        self.send({'jsonrpc': '2.0', 'id': 5, 'method': 'no.such.method', 'params': {}})
        message = self.receive()
        self.assertEqual((message['id'], message['error']['data']['code']), (5, 'METHOD_UNSUPPORTED'))
        self.send({'jsonrpc': '2.0', 'id': 6, 'method': 'health', 'params': {}})
        self.assertEqual(self.receive()['result']['status'], 'ready')

    def test_protocol_violations_end_the_worker(self):
        cases = {
            'credit beyond what was committed': lambda: self.send({'jsonrpc': '2.0', 'method': 'stream.credit', 'params': {
                'stream': 's', 'committed_seq': 1, 'bytes': 10, 'items': 1}}),
            'credit for another stream': lambda: self.send({'jsonrpc': '2.0', 'method': 'stream.credit', 'params': {
                'stream': 'zzz', 'committed_seq': 0}}),
            'cancel naming another attempt': lambda: self.cancel(attempt_id='other'),
            'cancel naming another generation': lambda: self.cancel(owner_generation=6),
            'second initialize': lambda: self.send({'jsonrpc': '2.0', 'id': 9, 'method': 'initialize', 'params': {
                'protocol': {'min': '1.0', 'max': '1.0'}, 'owner_generation': 7,
                'limits': {'max_message_bytes': framing.MAX_MESSAGE_BYTES}}}),
        }
        for name, action in cases.items():
            with self.subTest(name):
                self.tearDown_process()
                self.setUp_process()
                self.start('fixture.cooperative')
                action()
                self.assertEqual(self.process.wait(timeout=5), 1)

    def test_a_task_request_before_initialize_ends_the_worker(self):
        self.tearDown_process()
        self.setUp_process(initialize=False)
        self.start()
        self.assertEqual(self.process.wait(timeout=5), 1)

    def test_health_and_control_messages_before_initialize_end_the_worker(self):
        messages = [{'jsonrpc': '2.0', 'id': 2, 'method': 'health', 'params': {}},
                    {'jsonrpc': '2.0', 'method': 'task.cancel', 'params': {
                        'job_id': 'j', 'attempt_id': 'a', 'owner_generation': 7, 'job_version': 1}}]
        for message in messages:
            with self.subTest(method=message['method']):
                self.tearDown_process()
                self.setUp_process(initialize=False)
                self.send(message)
                self.assertEqual(self.process.wait(timeout=5), 1)

    def test_garbage_on_the_protocol_stream_ends_the_worker(self):
        for garbage in (b'\x00\x00\x00\x03abc', b'\xff\xff\xff\xff', b'\x00\x00\x00\x14{'):
            with self.subTest(garbage=garbage):
                self.tearDown_process()
                self.setUp_process()
                self.process.stdin.write(garbage)
                self.process.stdin.flush()
                if garbage.endswith(b'{'):
                    self.process.stdin.close()
                self.assertEqual(self.process.wait(timeout=5), 1)


if __name__ == '__main__':
    unittest.main()

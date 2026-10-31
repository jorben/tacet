import unittest

from tacet_plugin_sdk.schema import Schemas, SchemaError


class SchemaTest(unittest.TestCase):
    def setUp(self):
        self.schemas = Schemas()
        self.schemas.compile('rpc.schema.json', 'Message')
        self.schemas.compile('initialize.schema.json', 'Params')
        self.schemas.compile('manifest.schema.json')

    def test_same_embedded_contract_validates_envelopes(self):
        for value in ({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {}},
                      {'jsonrpc': '2.0', 'method': 'task.cancel', 'params': {}},
                      {'jsonrpc': '2.0', 'id': 'a', 'result': {}}):
            self.schemas.validate('rpc.schema.json', 'Message', value)

    def test_invalid_envelopes_return_no_raw_values(self):
        for value in ({'jsonrpc': '1.0', 'id': 1, 'result': {}},
                      {'jsonrpc': '2.0', 'id': None, 'result': {}},
                      {'jsonrpc': '2.0', 'id': 1, 'result': {}, 'error': {}},
                      {'jsonrpc': '2.0', 'id': 1, 'result': {}, 'sensitive': 'TEST_SECRET'}):
            with self.assertRaises(SchemaError) as raised:
                self.schemas.validate('rpc.schema.json', 'Message', value)
            self.assertEqual(str(raised.exception), 'PROTOCOL_SCHEMA_INVALID')

    def test_only_shipped_schema_definitions_can_compile(self):
        for name, definition in (('https://remote.invalid/schema', 'Message'),
                                 ('rpc.schema.json', 'Missing')):
            with self.assertRaises(SchemaError):
                self.schemas.compile(name, definition)


if __name__ == '__main__':
    unittest.main()

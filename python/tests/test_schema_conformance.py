import json
import unittest
from pathlib import Path

from tacet_plugin_sdk.schema import Schemas, SchemaError

VECTORS = Path(__file__).resolve().parents[2] / 'protocol/testdata/schema-conformance-v1.json'


class SchemaConformanceTest(unittest.TestCase):
    """Runs the vector file the Go host also runs (internal/host/schema_conformance_test.go)."""

    def test_python_agrees_with_every_shared_verdict(self):
        cases = json.loads(VECTORS.read_text(encoding='utf-8'))['cases']
        self.assertGreaterEqual(len(cases), 50)
        schemas = Schemas()
        for case in cases:
            schemas.compile(case['schema'], case['def'])
        for case in cases:
            with self.subTest(case['name']):
                try:
                    schemas.validate(case['schema'], case['def'], case['value'])
                    verdict = True
                except SchemaError:
                    verdict = False
                self.assertEqual(verdict, case['valid'])


if __name__ == '__main__':
    unittest.main()

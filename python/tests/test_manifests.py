"""Checks built-in plugin manifests against the required fields and enums of
protocol/schema/v1/manifest.schema.json without a JSON Schema dependency."""

import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCHEMA = json.loads((ROOT / "protocol/schema/v1/manifest.schema.json").read_text(encoding="utf-8"))
PLUGINS = sorted((ROOT / "python/plugins").glob("*/manifest.json"))


class ManifestTest(unittest.TestCase):
    def test_plugins_exist(self):
        self.assertTrue(PLUGINS)

    def test_manifests_follow_schema(self):
        props = SCHEMA["properties"]
        roles = set(props["roles"]["items"]["enum"])
        platforms = set(props["platforms"]["items"]["enum"])
        caps = set(props["capabilities"]["propertyNames"]["enum"])
        for path in PLUGINS:
            with self.subTest(plugin=path.parent.name):
                m = json.loads(path.read_text(encoding="utf-8"))
                for key in SCHEMA["required"] + ["roles", "runtime", "platforms"]:
                    self.assertIn(key, m)
                self.assertEqual(m["kind"], "plugin")
                self.assertEqual(m["runtime"]["kind"], "python")
                self.assertLessEqual(set(m["roles"]), roles)
                self.assertLessEqual(set(m["platforms"]), platforms)
                self.assertLessEqual(set(m.get("capabilities", {})), caps)
                self.assertEqual(set(m) - set(props), set())
                pkg = m["runtime"]["entry"]
                self.assertTrue((path.parent / "src" / pkg / "__init__.py").exists())


if __name__ == "__main__":
    unittest.main()

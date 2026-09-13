import importlib.util
from pathlib import Path
import tempfile
import unittest
import sys


@unittest.skipIf(sys.version_info < (3, 11), "Repair utility requires Python 3.11+")
class ProviderCompatibilityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("provider_compat", Path(__file__).resolve().parents[2] / "scripts/repair_provider_compatibility.py")
        cls.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.module)

    def test_preserves_original_bytes_and_defaults(self):
        original = b'# comment\r\nmodel = "custom-model"\r\n[projects."C:/test"]\r\ntrust_level = "trusted"\r\n'
        patched = self.module.patched_config(original)
        self.assertTrue(patched.startswith(original))
        self.assertNotIn(b"base_url", patched)
        self.assertNotIn(b"env_http_headers", patched)
        self.assertNotIn(b"model_provider =", patched)
        self.assertEqual(patched, self.module.patched_config(patched))

    def test_preserves_other_providers(self):
        original = b'[model_providers.other]\nname = "Other"\n'
        self.assertTrue(self.module.patched_config(original).startswith(original))

    def test_conflict_is_not_overwritten(self):
        with self.assertRaisesRegex(ValueError, "refusing"):
            self.module.patched_config(b'[model_providers.codex_router_spend]\nname = "Custom"\n')

    def test_invalid_toml_is_not_repaired_by_guessing(self):
        with self.assertRaises(ValueError):
            self.module.patched_config(b'invalid = [')

    def test_backup_and_idempotence(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "config.toml"
            original = b'# user config\n'
            path.write_bytes(original)
            backup = self.module.repair(path)
            self.assertEqual(backup.read_bytes(), original)
            self.assertIsNone(self.module.repair(path))
            self.assertEqual(len(list(Path(temp).glob("*.bak"))), 1)

    def test_missing_config_is_not_created(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaises(ValueError):
                self.module.repair(Path(temp) / "config.toml")


if __name__ == "__main__":
    unittest.main()

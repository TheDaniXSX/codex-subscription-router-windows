import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("renderer_9818", ROOT / "scripts/windows_renderer_26903.py")
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)


class NativeMenuBindingTests(unittest.TestCase):
    def test_updated_native_binding(self):
        native = '(0,xK.jsx)(Zy,{LeftIcon:oT,"aria-label":e,className:`opacity-50`,disabled:n,onSelect:r,children:f},`email`)'
        self.assertEqual(renderer.account_menu_item_alias(native, True), "Zy")

    def test_updated_binding_rejects_ambiguity(self):
        native = '(0,xK.jsx)(Zy,{LeftIcon:oT,"aria-label":e,className:`opacity-50`,disabled:n,onSelect:r,children:f},`email`)'
        with self.assertRaises(RuntimeError):
            renderer.account_menu_item_alias(native + native, True)

    def test_wrong_version_is_not_guessed(self):
        with self.assertRaises(RuntimeError):
            renderer.account_menu_item_alias('const Zy = {};', True)


if __name__ == "__main__":
    unittest.main()

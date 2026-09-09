import json
import importlib.util
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from test_patch_windows_app import patcher


class September903Tests(unittest.TestCase):
    def test_profile_item_binding_comes_from_native_menu_not_css_or_keyboard_map(self):
        spec = importlib.util.spec_from_file_location('renderer26903', patcher.PROJECT_ROOT / 'scripts/windows_renderer_26903.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        native = '(0,dq.jsx)(Qy,{LeftIcon:CT,"aria-label":e,className:`opacity-50`,disabled:n,onSelect:r,children:f},`email`)'
        self.assertEqual(module.account_menu_item_alias('const xl={};"text-xl";' + native), 'Qy')
        with self.assertRaisesRegex(RuntimeError, 'found 0'):
            module.account_menu_item_alias('const xl={};"text-xl";')
        with self.assertRaisesRegex(RuntimeError, 'found 2'):
            module.account_menu_item_alias(native + native)

    def test_signed_runtime_is_preserved_without_resource_rebinding(self):
        desktop = patcher.TESTED_SOURCE_BUILDS['26.903.8094.0']['chatgpt_sha256']
        chrome = 'c2fb95027940a26eac4bd541a0cde66d0591af67e0f3c4efdb30e5ec6c98cd76'
        with mock.patch.object(patcher, 'sha256_file', side_effect=[desktop, chrome, chrome, desktop]):
            self.assertIsNone(patcher.rebind_desktop_integrity(Path('stage'), Path('source')))
        with mock.patch.object(patcher, 'sha256_file', side_effect=[desktop, chrome, 'changed']):
            with self.assertRaisesRegex(RuntimeError, 'byte-for-byte'):
                patcher.rebind_desktop_integrity(Path('stage'), Path('source'))

    def test_exact_fingerprint_required(self):
        expected = patcher.TESTED_SOURCE_BUILDS['26.903.8094.0']
        source = SimpleNamespace(package_version='26.903.8094.0', **expected)
        patcher.validate_approved_source(source, False)
        source.asar_sha256 = '0' * 64
        with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
            patcher.validate_approved_source(source, False)

    def test_native_profile_is_version_scoped(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            assets = root / 'webview' / 'assets'
            assets.mkdir(parents=True)
            (assets / 'app-primary-test.js').write_text('', encoding='utf-8')
            (root / 'package.json').write_text(json.dumps({'version': '26.903.61454'}), encoding='utf-8')
            self.assertEqual(patcher._native_anchor(root, 'oY sY jq Oq Fy yJ bJ Mq'), 'MZ NZ rX eX Ob GX KX iX')
            (root / 'package.json').write_text(json.dumps({'version': '26.901.51231'}), encoding='utf-8')
            self.assertEqual(patcher._native_anchor(root, 'oY sY jq Oq Fy yJ bJ Mq'), 'LZ RZ cX aX Pb XX ZX lX')

    def test_new_appshots_stays_opt_in(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            build = root / '.vite' / 'build'
            build.mkdir(parents=True)
            assets = root / 'webview' / 'assets'
            assets.mkdir(parents=True)
            (assets / 'app-primary-test.js').write_text('', encoding='utf-8')
            (root / 'package.json').write_text(json.dumps({'version': '26.903.61454'}), encoding='utf-8')
            main = build / 'main-test.js'
            main.write_text('ae=v&&a.i.isInternal(t)?RIe(h):null,oe=new DIe;' +
                            'let n=U(),r=n.skysight,o=pr(e);P&&B.windowsCaptureNativeBridge==null&&(o.appshotsEnabled=!1),P&&!a.i.isInternal(c)&&(o.appshotsEnabled=!1),Be.setDesktopFeatureAvailability(o);', encoding='utf-8')
            patcher.patch_windows_appshots_gate(root)
            contract = patcher.verify_windows_appshots_contract(root)
            self.assertFalse(contract['defaultEnabled'])
            self.assertTrue(contract['requiresNativeBridge'])
            with self.assertRaises(RuntimeError):
                patcher.patch_windows_appshots_gate(root)

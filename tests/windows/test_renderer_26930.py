"""Fail-closed 26.930 anchors and optional offline official-bundle contract."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import windows_renderer_26930 as renderer

SOURCE = Path(os.environ.get("CODEX_RENDERER_26930_SOURCE", str(ROOT.parent / "router-source-26930")))
FILES = (
    "package.json", "webview/index.html",
    "webview/assets/app-initial-146770bfc1f4.js",
    "webview/assets/app-shared-e20a5fe9db04.js",
    "webview/assets/profile-dropdown-items-54942f4c1933.js",
    "webview/assets/modal-impl-608aec7bbe14.js",
    "webview/assets/profile-27a86d4aa2f3.js",
    "webview/assets/plugins-settings-29dca9faafcb.js",
    "webview/assets/local-conversation-thread-cbc63677a2bf.js",
)


class Renderer26930Tests(unittest.TestCase):
    def test_native_menu_item_fails_closed(self):
        anchor = '(0,$.jsx)(U,{leftIconAsset:tt,"aria-label":e,className:`opacity-50`,disabled:t,onSelect:r,children:v},`email`)'
        self.assertEqual(renderer.account_menu_item_alias(anchor), "U")
        for text in (anchor + anchor, anchor.replace("disabled:t", "disabled:n"), ""):
            with self.subTest(text=text[:30]), self.assertRaises(RuntimeError):
                renderer.account_menu_item_alias(text)

    def test_function_boundaries_fail_closed(self):
        source = "function A(){old()}function B(e){keep()}"
        self.assertEqual(renderer._replace_function(source, "function A(){", "function B(e)", "new;"), "new;function B(e){keep()}")
        for text in (source + source, source.replace("function B(e)", "function C(e)")):
            with self.assertRaises(RuntimeError):
                renderer._replace_function(text, "function A(){", "function B(e)", "new;")

    def test_unsupported_version_and_unsafe_parameters_fail_before_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            manifest = root / "package.json"
            manifest.write_text(json.dumps({"version": "26.931.0"}), encoding="utf8")
            with self.assertRaisesRegex(RuntimeError, "Unsupported"):
                renderer.patch_renderer(root, "x" * 64, 55876)
            manifest.write_text(json.dumps({"version": renderer.VERSION}), encoding="utf8")
            for token, port in (("bad`token", 55876), ("x" * 64, 0), ("x" * 64, 65536)):
                with self.assertRaisesRegex(RuntimeError, "Invalid"):
                    renderer.patch_renderer(root, token, port)
            self.assertEqual(list(root.iterdir()), [manifest])

    @unittest.skipUnless((SOURCE / FILES[2]).is_file(), "official extracted 26.930 source not supplied")
    def test_real_official_bundles_patch_parse_and_packed_contract(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in FILES:
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(SOURCE / name, target)
            # Fail at the final index anchor and verify earlier JS is unchanged.
            index = root / "webview/index.html"
            original = index.read_text(encoding="utf8")
            index.write_text(original.replace("connect-src &#39;self&#39;", "changed-connect-policy"), encoding="utf8")
            before = {name: (root / name).read_bytes() for name in FILES}
            with self.assertRaises(RuntimeError):
                renderer.patch_renderer(root, "x" * 64, 55876)
            self.assertEqual(before, {name: (root / name).read_bytes() for name in FILES})
            index.write_text(original, encoding="utf8")
            # The active sidebarFooter branch must also be version locked.
            # Losing its additionalItems slot must not publish a legacy-only
            # patch that passes parsing but hides account management in the UI.
            menu = root / FILES[4]
            original_menu = menu.read_text(encoding="utf8")
            menu.write_text(original_menu.replace("children:[qn,J,null,m,h]", "children:[qn,J,null,h,m]", 1), encoding="utf8")
            before = {name: (root / name).read_bytes() for name in FILES}
            with self.assertRaisesRegex(RuntimeError, "anchor count 0"):
                renderer.patch_renderer(root, "x" * 64, 55876)
            self.assertEqual(before, {name: (root / name).read_bytes() for name in FILES})
            menu.write_text(original_menu, encoding="utf8")
            renderer.patch_renderer(root, "x" * 64, 55876)
            for name in FILES[2:]:
                # Never echo parser diagnostics containing an injected token.
                parsed = subprocess.run(["node", "--input-type=module", "--check"], input=(root / name).read_bytes(), capture_output=True, timeout=30)
                self.assertEqual(parsed.returncode, 0, f"patched JS parses: {name}")
            script = "const fs=require('node:fs');const path=require('node:path');const root=process.argv[1];const entries=JSON.parse(process.argv[2]).map(v=>'/'+v);const asar={extractFile:(_,name)=>fs.readFileSync(path.join(root,name))};const initial=asar.extractFile(null,'webview/assets/app-initial-146770bfc1f4.js').toString();require(process.argv[3])({asar,archive:null,entries,initial}).catch(e=>{console.error(String(e.message).split('\\n')[0]);process.exitCode=1});"
            result = subprocess.run(["node", "-e", script, str(root), json.dumps(FILES), str(ROOT / "tests/windows/profile-menu-render-26930.cjs")], capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()

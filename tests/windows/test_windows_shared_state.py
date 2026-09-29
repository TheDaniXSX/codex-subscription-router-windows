"""Exact-version shared desktop state patch contracts; no installed files touched."""

import json
from pathlib import Path
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import windows_shared_state as patcher


class SharedGlobalStatePatchTests(unittest.TestCase):
    def make_fixture(self, root):
        root = Path(root)
        (root / "package.json").write_text(json.dumps({"version": "26.924.22138"}), encoding="utf-8")
        build = root / ".vite" / "build"
        assets = root / "webview" / "assets"
        build.mkdir(parents=True)
        assets.mkdir(parents=True)
        sources = {
            build / "policy-fixture.js": "var wr=class{logger=r.Lt(`global-state`);" + patcher.CONSTRUCTOR + "}",
            build / "main-fixture.js": "\n".join([
                patcher.SYNC_METHOD, patcher.ATOM_METHOD, patcher.GLOBAL_GET, patcher.GLOBAL_SET,
                "this.updatePersistedAtomState(e,t.key,t.deleted?void 0:t.value,t.recordUpdate)",
            ]),
            assets / "app-initial-fixture.js": "\n".join([patcher.ATOM_SEND, patcher.ATOM_SYNC, patcher.ATOM_UPDATE]),
            assets / "app-shared-fixture.js": "\n".join([patcher.GLOBAL_TRANSFORM_GET, patcher.GLOBAL_TRANSFORM_SET, patcher.GLOBAL_DIRECT_SET]),
        }
        for file, source in sources.items():
            file.write_text(source, encoding="utf-8")
        return sources

    def test_exact_profile_packages_helper_and_requires_renderer_baselines(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_fixture(root)
            result = patcher.patch_shared_global_state(root)
            self.assertEqual(result["protocol"], "filesystem-leaf-cas-v1")
            self.assertEqual(result["rendererWrites"], "snapshot-required-leaf-cas")
            self.assertEqual(result["arrays"], "atomic-compare-and-set")
            helper = root / result["helper"]
            self.assertEqual(helper.read_text(encoding="utf-8"), (ROOT / "ui" / "shared-global-state.cjs").read_text(encoding="utf-8"))
            main = (root / ".vite/build/main-fixture.js").read_text(encoding="utf-8")
            self.assertIn(".setRendererGlobal(this,e,t,n)", main)
            self.assertIn(".setRendererAtom(this,e,t,n,r,s,uVe)", main)
            self.assertIn("await this.updatePersistedAtomState(e,t.key,t.deleted?void 0:t.value,t.recordUpdate,t.csrBase)", main)
            initial = (root / "webview/assets/app-initial-fixture.js").read_text(encoding="utf-8")
            self.assertIn("csrSharedAtomBases=JSON.parse(JSON.stringify(s??{}))", initial)
            self.assertIn("recordUpdate:n,csrBase", initial)
            shared = (root / "webview/assets/app-shared-fixture.js").read_text(encoding="utf-8")
            self.assertIn("csrBase:a?.csrBase", shared)
            self.assertIn("csrBase:c", shared)

    def test_missing_or_duplicate_anchors_never_partially_write(self):
        for anchor in [patcher.CONSTRUCTOR, patcher.SYNC_METHOD, patcher.ATOM_METHOD,
                       patcher.GLOBAL_GET, patcher.GLOBAL_SET, patcher.ATOM_SEND,
                       patcher.ATOM_SYNC, patcher.ATOM_UPDATE, patcher.GLOBAL_TRANSFORM_GET,
                       patcher.GLOBAL_TRANSFORM_SET, patcher.GLOBAL_DIRECT_SET]:
            for duplicate in [False, True]:
                with self.subTest(anchor=anchor[:45], duplicate=duplicate), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    sources = self.make_fixture(root)
                    target = next(file for file, text in sources.items() if anchor in text)
                    sources[target] = sources[target].replace(anchor, anchor + anchor if duplicate else "ANCHOR_DRIFT")
                    target.write_text(sources[target], encoding="utf-8")
                    with self.assertRaises(RuntimeError):
                        patcher.patch_shared_global_state(root)
                    for file, source in sources.items():
                        self.assertEqual(file.read_text(encoding="utf-8"), source)
                    self.assertFalse((root / ".vite/build" / patcher.HELPER_NAME).exists())

    def test_unsupported_version_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sources = self.make_fixture(root)
            (root / "package.json").write_text('{"version":"26.925.0"}', encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "only reviewed"):
                patcher.patch_shared_global_state(root)
            for file, source in sources.items():
                self.assertEqual(file.read_text(encoding="utf-8"), source)

    def test_duplicate_install_is_rejected_without_rewriting(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sources = self.make_fixture(root)
            patcher.patch_shared_global_state(root)
            before = {file: file.read_text(encoding="utf-8") for file in sources}
            with self.assertRaises(RuntimeError):
                patcher.patch_shared_global_state(root)
            self.assertEqual(before, {file: file.read_text(encoding="utf-8") for file in sources})


if __name__ == "__main__":
    unittest.main()

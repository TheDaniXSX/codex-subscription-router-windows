from __future__ import annotations

import hashlib
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]


class DependencyNoticeTests(unittest.TestCase):
    def sections(self) -> list[tuple[str, str, str, str]]:
        bundle = (ROOT / "assets" / "SQLITE-LICENSE.txt").read_text(encoding="utf-8")
        pattern = (
            r"(?m)^={78}\nModule: ([^\n]+)\nSource: ([^\n]+)\n"
            r"SHA-256 \(LF text\): ([0-9a-f]{64})\n-{78}\n"
            r"(.*?)(?=\n={78}\n|\Z)"
        )
        return re.findall(pattern, bundle, re.S)

    def test_every_pinned_module_has_its_binary_redistribution_notice(self) -> None:
        go_mod = (ROOT / "go.mod").read_text(encoding="utf-8")
        modules = re.findall(r"^\s*(?:require\s+)?([\w.\-/]+)\s+(v\S+)", go_mod, re.M)
        expected = {f"{module}@{version}" for module, version in modules}
        actual = {module for module, source, _digest, _body in self.sections() if source == "LICENSE" and not module.startswith("Go@")}
        self.assertTrue(expected, "go.mod should have the SQLite dependency graph")
        self.assertEqual(actual, expected, "Refresh the bundled notices whenever go.mod dependencies change")
        go_version = re.search(r"^go\s+(\S+)", go_mod, re.M).group(1)
        self.assertIn(f"Go@{go_version}", {entry[0] for entry in self.sections()})

    def test_upstream_text_checksums_and_embedded_notices_are_preserved(self) -> None:
        sections = self.sections()
        self.assertEqual(len(sections), 19)
        for module, source, digest, body in sections:
            with self.subTest(module=module, source=source):
                normalized = body.rstrip() + "\n"
                self.assertEqual(hashlib.sha256(normalized.encode("utf-8")).hexdigest(), digest)
                self.assertGreater(len(normalized), 500)
        sources = {source for _module, source, _digest, _body in sections}
        self.assertTrue({"SQLITE-LICENSE", "LICENSE-GO", "COPYRIGHT-MUSL",
                         "honnef.co/go/netdb/LICENSE", "LICENSE-MMAP-GO",
                         "libc_windows.go (Regents notice)"}.issubset(sources))


if __name__ == "__main__":
    unittest.main()

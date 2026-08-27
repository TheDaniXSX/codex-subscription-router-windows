from __future__ import annotations

import importlib.util
import base64
import json
import os
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
MODULE_PATH = REPOSITORY_ROOT / "scripts" / "release" / "release_artifacts.py"
BUILD_SCRIPT = REPOSITORY_ROOT / "scripts" / "release" / "build_source_release.py"
VERIFY_SCRIPT = REPOSITORY_ROOT / "scripts" / "release" / "verify_source_release.py"
SPEC = importlib.util.spec_from_file_location("release_artifacts", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
release_artifacts = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = release_artifacts
SPEC.loader.exec_module(release_artifacts)


class SourceReleaseTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name) / "repository"
        self.root.mkdir()
        self._write("VERSION", "1.2.3\n")
        self._write(
            "package-lock.json",
            json.dumps(
                {
                    "lockfileVersion": 3,
                    "packages": {
                        "": {"name": "fixture", "version": "1.2.3"},
                        "node_modules/example": {
                            "version": "2.0.0",
                            "license": "MIT",
                            "integrity": "sha512-"
                            + base64.b64encode(bytes(64)).decode("ascii"),
                        },
                    },
                }
            ),
        )
        self._write("go.mod", "module example.invalid/fixture\n\ngo 1.26.0\n")
        self._write("README.md", "synthetic source fixture\n")
        self._write("tool.sh", "#!/bin/sh\nexit 0\n")
        self._write(
            "docs/E2E-REPORT-WINDOWS.md",
            "\n".join(
                (
                    "<!-- release-version: 1.2.3 -->",
                    "<!-- release-qualification: NOT QUALIFIED -->",
                    "<!-- release-commit: PENDING -->",
                    "<!-- release-date: PENDING -->",
                    "- [ ] live qualification not run in this source fixture",
                    "",
                )
            ),
        )
        self._run("git", "init", "-q")
        self._run("git", "config", "user.email", "fixture@example.invalid")
        self._run("git", "config", "user.name", "Fixture")
        self._run("git", "add", ".")
        env = os.environ.copy()
        env["GIT_AUTHOR_DATE"] = "2026-01-02T03:04:05Z"
        env["GIT_COMMITTER_DATE"] = "2026-01-02T03:04:05Z"
        subprocess.run(
            ["git", "commit", "-q", "-m", "fixture"],
            cwd=self.root,
            check=True,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.source_gates = Path(self.temporary.name) / "source-gates.json"
        release_artifacts.create_source_gate_evidence(self.root, self.source_gates)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write(self, relative: str, value: str | bytes) -> None:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(value, bytes):
            path.write_bytes(value)
        else:
            path.write_text(value, encoding="utf-8", newline="\n")

    def _run(self, *args: str) -> None:
        subprocess.run(
            list(args),
            cwd=self.root,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def test_build_is_reproducible_and_verifies(self) -> None:
        first = Path(self.temporary.name) / "first"
        second = Path(self.temporary.name) / "second"
        release_artifacts.build_release(
            self.root, first, source_gates_path=self.source_gates
        )
        release_artifacts.build_release(
            self.root, second, source_gates_path=self.source_gates
        )

        self.assertEqual(
            {item.name: item.read_bytes() for item in first.iterdir()},
            {item.name: item.read_bytes() for item in second.iterdir()},
        )
        verified = release_artifacts.verify_release(self.root, first)
        self.assertEqual(6, len(verified))

        source_gates = json.loads(
            (
                first / "codex-subscription-router-windows-v1.2.3.source-gates.json"
            ).read_text(encoding="utf-8")
        )
        self.assertEqual("AUTOMATED_GATES_PASSED", source_gates["status"])
        self.assertEqual("source-gates", source_gates["scope"])

        provenance = json.loads(
            (
                first / "codex-subscription-router-windows-v1.2.3.provenance.json"
            ).read_text(encoding="utf-8")
        )
        parameters = provenance["predicate"]["buildDefinition"]["externalParameters"]
        self.assertEqual("AUTOMATED_GATES_PASSED", parameters["automatedGateStatus"])
        self.assertEqual("source-gates", parameters["evidenceScope"])

        cyclonedx = json.loads(
            (first / "codex-subscription-router-windows-v1.2.3.cdx.json").read_text(
                encoding="utf-8"
            )
        )
        self.assertIn(
            "TheDaniXSX/codex-subscription-router-windows",
            cyclonedx["metadata"]["component"]["purl"],
        )

        archive = first / "codex-subscription-router-windows-v1.2.3.tar.gz"
        with tarfile.open(archive, "r:gz") as source:
            names = source.getnames()
        self.assertEqual(names, sorted(names))
        self.assertTrue(
            all(
                name.startswith("codex-subscription-router-windows-v1.2.3/")
                for name in names
            )
        )

    def test_command_line_build_and_verify(self) -> None:
        output = Path(self.temporary.name) / "command-line"
        subprocess.run(
            [
                sys.executable,
                str(BUILD_SCRIPT),
                "--source-root",
                str(self.root),
                "--output-dir",
                str(output),
                "--source-gates",
                str(self.source_gates),
            ],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        result = subprocess.run(
            [
                sys.executable,
                str(VERIFY_SCRIPT),
                "--source-root",
                str(self.root),
                "--input-dir",
                str(output),
            ],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.assertIn("verified 6 source-only release files", result.stdout)

    def test_dirty_tree_is_rejected_and_never_archived(self) -> None:
        self._write("auth.json", '{"token":"not-real"}\n')
        output = Path(self.temporary.name) / "output"
        with self.assertRaisesRegex(
            release_artifacts.ReleaseError, "working tree is dirty"
        ):
            release_artifacts.build_release(
                self.root, output, source_gates_path=self.source_gates
            )

        release_artifacts.build_release(
            self.root,
            output,
            source_gates_path=self.source_gates,
            allow_dirty=True,
        )
        archive = output / "codex-subscription-router-windows-v1.2.3.tar.gz"
        with tarfile.open(archive, "r:gz") as source:
            self.assertFalse(
                any(name.endswith("auth.json") for name in source.getnames())
            )

    def test_committed_official_payload_is_rejected(self) -> None:
        self._write("resources/app.asar", b"synthetic-not-openai")
        self._run("git", "add", "resources/app.asar")
        self._run("git", "commit", "-q", "-m", "forbidden")
        with self.assertRaisesRegex(
            release_artifacts.ReleaseError, "payload is forbidden"
        ):
            release_artifacts.build_release(
                self.root,
                Path(self.temporary.name) / "output",
                source_gates_path=self.source_gates,
            )

    def test_forbidden_scan_rejects_disguised_payloads_and_secret_state(self) -> None:
        cases = (
            ("fixtures/tool.dat", b"MZ" + bytes(100)),
            ("fixtures/archive.dat", b"PK\x03\x04" + bytes(100)),
            ("local/credentials-production.json", b"{}"),
            ("local/session.token", b"not-a-real-token"),
            ("OpenAI.Codex_example/resources/readme.txt", b"metadata"),
        )
        for relative, data in cases:
            with self.subTest(relative=relative):
                with self.assertRaises(release_artifacts.ReleaseError):
                    release_artifacts.scan_source_file(relative, data)

    def test_source_gates_for_another_commit_are_rejected(self) -> None:
        value = json.loads(self.source_gates.read_text(encoding="utf-8"))
        value["sourceCommit"] = "b" * 40
        self.source_gates.write_text(json.dumps(value), encoding="utf-8")
        with self.assertRaisesRegex(
            release_artifacts.ReleaseError, "does not exactly match"
        ):
            release_artifacts.build_release(
                self.root,
                Path(self.temporary.name) / "output",
                source_gates_path=self.source_gates,
            )

    def test_tampered_artifact_is_rejected(self) -> None:
        output = Path(self.temporary.name) / "output"
        release_artifacts.build_release(
            self.root, output, source_gates_path=self.source_gates
        )
        archive = output / "codex-subscription-router-windows-v1.2.3.tar.gz"
        archive.write_bytes(archive.read_bytes() + b"tampered")
        with self.assertRaisesRegex(
            release_artifacts.ReleaseError, "hash/size mismatch"
        ):
            release_artifacts.verify_release(self.root, output)

    def test_noncanonical_manifest_is_rejected(self) -> None:
        output = Path(self.temporary.name) / "output"
        release_artifacts.build_release(
            self.root, output, source_gates_path=self.source_gates
        )
        manifest = output / "SHA256SUMS"
        first, *remaining = manifest.read_text(encoding="utf-8").splitlines()
        digest, size, name = first.split("  ", 2)
        manifest.write_text(
            "\n".join((f"{digest}  0{size}  {name}", *remaining, "")),
            encoding="utf-8",
            newline="\n",
        )
        with self.assertRaisesRegex(release_artifacts.ReleaseError, "canonical"):
            release_artifacts.verify_release(self.root, output)

    def test_archive_must_match_commit_even_with_rehashed_metadata(self) -> None:
        output = Path(self.temporary.name) / "output"
        release_artifacts.build_release(
            self.root, output, source_gates_path=self.source_gates
        )
        archive = output / "codex-subscription-router-windows-v1.2.3.tar.gz"
        original = archive.read_bytes()
        archive.write_bytes(original[:-8] + b"changed!")
        artifacts = []
        for item in output.iterdir():
            if item.name not in {"SHA256SUMS"}:
                artifacts.append((item.name, item.read_bytes()))
        (output / "SHA256SUMS").write_bytes(
            release_artifacts._manifest_bytes(artifacts)
        )
        with self.assertRaises(release_artifacts.ReleaseError):
            release_artifacts.verify_release(self.root, output)


if __name__ == "__main__":
    unittest.main()

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "check_release", ROOT / "scripts" / "check_release.py"
)
assert SPEC is not None and SPEC.loader is not None
CHECK_RELEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK_RELEASE)


def report(
    *,
    status: str = "NOT QUALIFIED",
    version: str = "0.2.0",
    commit: str = "PENDING",
    date: str = "PENDING",
    unchecked: bool = True,
) -> str:
    checklist = "- [ ] pending" if unchecked else "- [x] passed"
    return "\n".join(
        (
            f"<!-- release-version: {version} -->",
            f"<!-- release-qualification: {status} -->",
            f"<!-- release-commit: {commit} -->",
            f"<!-- release-date: {date} -->",
            checklist,
        )
    )


class ReleaseReportTests(unittest.TestCase):
    def test_template_report_is_valid_for_release_metadata(self) -> None:
        status = CHECK_RELEASE.validate_qualification(report(), "0.2.0")
        self.assertEqual(status, "NOT QUALIFIED")

    def test_unknown_report_status_is_rejected(self) -> None:
        with self.assertRaises(SystemExit):
            CHECK_RELEASE.validate_qualification(
                report(status="UNKNOWN"), "0.2.0"
            )

    def test_qualified_report_requires_complete_evidence(self) -> None:
        commit = "a" * 40
        status = CHECK_RELEASE.validate_qualification(
            report(
                status="QUALIFIED",
                commit=commit,
                date="2026-08-27",
                unchecked=False,
            ),
            "0.2.0",
        )
        self.assertEqual(status, "QUALIFIED")

    def test_qualified_report_rejects_unchecked_gate(self) -> None:
        commit = "b" * 40
        with self.assertRaises(SystemExit):
            CHECK_RELEASE.validate_qualification(
                report(
                    status="QUALIFIED",
                    commit=commit,
                    date="2026-08-27",
                ),
                "0.2.0",
            )

    def test_duplicate_marker_is_rejected(self) -> None:
        duplicated = report() + "\n<!-- release-version: 0.2.0 -->\n"
        with self.assertRaises(SystemExit):
            CHECK_RELEASE.release_marker(duplicated, "release-version")


if __name__ == "__main__":
    unittest.main()

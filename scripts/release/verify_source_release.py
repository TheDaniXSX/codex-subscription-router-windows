#!/usr/bin/env python3
"""Verify a source-only release artifact set without extracting it."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from release_artifacts import ReleaseError, verify_release


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="Git worktree whose commit/version the artifacts must describe",
    )
    parser.add_argument("--input-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        outputs = verify_release(args.source_root, args.input_dir)
    except (ReleaseError, OSError, ValueError) as exc:
        print(f"source release verification failed: {exc}", file=sys.stderr)
        return 1
    print(f"verified {len(outputs)} source-only release files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

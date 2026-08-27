#!/usr/bin/env python3
"""Generate deterministic source-only release artifacts."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from release_artifacts import ReleaseError, build_release


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="Git worktree to package (default: repository containing this script)",
    )
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument(
        "--source-gates",
        type=Path,
        required=True,
        help="CI-generated automated source-gate JSON for the exact commit",
    )
    parser.add_argument(
        "--allow-dirty",
        action="store_true",
        help="package HEAD even if the worktree has changes (never includes those changes)",
    )
    args = parser.parse_args()
    try:
        outputs = build_release(
            args.source_root,
            args.output_dir,
            source_gates_path=args.source_gates,
            allow_dirty=args.allow_dirty,
        )
    except (ReleaseError, OSError, ValueError) as exc:
        print(f"source release build failed: {exc}", file=sys.stderr)
        return 1
    for output in outputs:
        print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Create commit-bound evidence that automated source gates passed in CI."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from release_artifacts import ReleaseError, create_source_gate_evidence


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="gated Git worktree (default: repository containing this script)",
    )
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        output = create_source_gate_evidence(args.source_root, args.output)
    except (ReleaseError, OSError, ValueError) as exc:
        print(f"source-gate evidence creation failed: {exc}", file=sys.stderr)
        return 1
    print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

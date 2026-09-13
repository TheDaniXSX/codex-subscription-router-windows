#!/usr/bin/env python3
"""Register the router's history provider for use outside the router (Python 3.11+).

No default provider, URL, credentials, gateway token, or history is changed.
The router's process-local -c overrides still replace this direct OpenAI alias.
"""

import argparse
import os
from pathlib import Path
import tempfile
import tomllib
import uuid

PROVIDER = "codex_router_spend"
DIRECT_PROVIDER = {
    "name": "OpenAI",
    "wire_api": "responses",
    "requires_openai_auth": True,
    "supports_websockets": True,
}
BLOCK = """
# Router history compatibility: outside the router, use this app's OpenAI login.
# No proxy address or token is persisted. The router overrides this per process.
[model_providers.codex_router_spend]
name = "OpenAI"
wire_api = "responses"
requires_openai_auth = true
supports_websockets = true
"""


def patched_config(original: bytes) -> bytes:
    config = tomllib.loads(original.decode("utf-8-sig"))
    existing = config.get("model_providers", {}).get(PROVIDER)
    if existing is not None:
        if existing != DIRECT_PROVIDER:
            raise ValueError("Existing router provider differs; refusing to overwrite it")
        return original
    newline = b"\r\n" if b"\r\n" in original else b"\n"
    result = original + newline + BLOCK.strip().encode().replace(b"\n", newline) + newline
    parsed = tomllib.loads(result.decode("utf-8-sig"))
    expected = dict(config)
    expected["model_providers"] = {**config.get("model_providers", {}), PROVIDER: DIRECT_PROVIDER}
    if parsed != expected:
        raise ValueError("Unexpected config change; refusing to write")
    return result


def repair(config_path: Path) -> Path | None:
    # Require an existing regular config; do not guess a home or traverse a link.
    if config_path.is_symlink() or not config_path.is_file():
        raise ValueError("Expected an existing regular config.toml")
    original = config_path.read_bytes()
    result = patched_config(original)
    if result == original:
        return None
    backup = config_path.with_name(f"config.toml.router-compat-{uuid.uuid4().hex}.bak")
    with backup.open("xb") as stream:
        os.chmod(backup, 0o600)
        stream.write(original)
        stream.flush()
        os.fsync(stream.fileno())
    fd, temporary = tempfile.mkstemp(prefix=".router-compat-", dir=config_path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(result)
            stream.flush()
            os.fsync(stream.fileno())
        if config_path.read_bytes() != original:
            raise RuntimeError("Config changed during repair; retry after closing settings")
        os.replace(temporary, config_path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return backup


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--apply", action="store_true", help="Back up and update the config")
    args = parser.parse_args()
    if args.apply:
        backup = repair(args.config)
        print(f"Compatibility alias installed. Backup: {backup}" if backup else "Already compatible; unchanged.")
    else:
        original = args.config.read_bytes()
        print("Already compatible." if patched_config(original) == original else "Compatibility alias needed; use --apply.")


if __name__ == "__main__":
    main()

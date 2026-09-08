"""Rebind Electron's named PE integrity resource without changing executable code.

Only the existing 64 ASCII hash bytes may differ from the preserved signed
original. Integrity enforcement and all other resource/code bytes remain intact.
The resulting local executable is intentionally no longer Authenticode-valid.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import mmap
from pathlib import Path
import re
import struct


MAX_PE_BYTES = 128 * 1024 * 1024
MAX_HEADER_BYTES = 128 * 1024 * 1024
FUSE_SENTINEL = b"dL7pKGdnNz796PbbjQWNKmHXBZaB9tsX"


def verify_integrity_fuses(runtime_directory: Path) -> None:
    library = runtime_directory / "chrome.dll"
    if (library.is_symlink() or not library.is_file() or
            not 64 <= library.stat().st_size <= 512 * 1024 * 1024):
        raise ValueError("missing bounded Electron runtime for fuse verification")
    with library.open("rb") as handle, mmap.mmap(handle.fileno(), 0, access=mmap.ACCESS_READ) as data:
        offset = data.find(FUSE_SENTINEL)
        if offset < 0 or data.find(FUSE_SENTINEL, offset + 1) >= 0:
            raise ValueError("expected one Electron fuse wire")
        wire = offset + len(FUSE_SENTINEL)
        if wire + 2 > len(data) or data[wire] != 1:
            raise ValueError("unsupported Electron fuse wire version")
        count = data[wire + 1]
        if count < 6 or wire + 2 + count > len(data):
            raise ValueError("truncated Electron fuse wire")
        states = data[wire + 2:wire + 2 + count]
        if states[4:6] != b"11":
            raise ValueError("embedded ASAR validation and ASAR-only loading must remain enabled")


def _read_pe(path: Path) -> bytes:
    if path.is_symlink() or not path.is_file() or not 256 <= path.stat().st_size <= MAX_PE_BYTES:
        raise ValueError("integrity executable is not a bounded regular PE file")
    return path.read_bytes()


def asar_header_sha256(path: Path) -> str:
    with path.open("rb") as handle:
        prefix = handle.read(16)
        if len(prefix) != 16:
            raise ValueError("truncated ASAR header")
        size_payload, header_size, header_payload, text_size = struct.unpack("<IIII", prefix)
        if (size_payload != 4 or header_size != header_payload + 4 or
                not 0 < text_size <= MAX_HEADER_BYTES or
                header_size != 8 + ((text_size + 3) & ~3) or
                8 + header_size > path.stat().st_size):
            raise ValueError("invalid ASAR header framing")
        header = handle.read(text_size)
        if len(header) != text_size:
            raise ValueError("truncated ASAR header JSON")
        parsed = json.loads(header)
        if not isinstance(parsed, dict) or not isinstance(parsed.get("files"), dict):
            raise ValueError("invalid ASAR file index")
        return hashlib.sha256(header).hexdigest()


def integrity_hash_slot(data: bytes) -> tuple[int, str]:
    """Resolve INTEGRITY/ELECTRONASAR through PE directories, never text search."""
    def unpack(fmt: str, offset: int):
        size = struct.calcsize(fmt)
        if offset < 0 or offset + size > len(data):
            raise ValueError("truncated PE structure")
        return struct.unpack_from(fmt, data, offset)

    if data[:2] != b"MZ":
        raise ValueError("missing PE DOS signature")
    pe, = unpack("<I", 0x3C)
    if data[pe:pe + 4] != b"PE\0\0":
        raise ValueError("missing PE signature")
    sections_count, = unpack("<H", pe + 6)
    optional_size, = unpack("<H", pe + 20)
    optional = pe + 24
    magic, = unpack("<H", optional)
    if magic != 0x20B or not 1 <= sections_count <= 96 or optional_size < 136:
        raise ValueError("expected bounded PE32+ resource layout")
    directory_count, = unpack("<I", optional + 108)
    if directory_count < 3:
        raise ValueError("PE has no resource directory")
    resource_rva, resource_size = unpack("<II", optional + 128)
    sections = []
    for index in range(sections_count):
        entry = optional + optional_size + index * 40
        virtual_size, virtual_address, raw_size, raw_offset = unpack("<IIII", entry + 8)
        if raw_offset + raw_size > len(data):
            raise ValueError("PE section exceeds executable")
        sections.append((virtual_address, raw_size, raw_offset))

    def file_offset(rva: int, size: int) -> int:
        matches = [raw + rva - address for address, raw_size, raw in sections
                   if address <= rva and rva + size <= address + raw_size]
        if len(matches) != 1:
            raise ValueError("ambiguous or unmapped PE resource RVA")
        return matches[0]

    if not 16 <= resource_size <= MAX_PE_BYTES:
        raise ValueError("invalid PE resource size")
    base = file_offset(resource_rva, resource_size)

    def resource_offset(relative: int, size: int) -> int:
        if relative < 0 or relative + size > resource_size:
            raise ValueError("resource directory reference outside resource section")
        return base + relative

    def directory(relative: int) -> list[tuple[str | int, int]]:
        offset = resource_offset(relative, 16)
        named, numbered = unpack("<HH", offset + 12)
        count = named + numbered
        if count > 4096:
            raise ValueError("excessive resource directory entries")
        resource_offset(relative + 16, count * 8)
        result = []
        for index in range(count):
            name, target = unpack("<II", offset + 16 + index * 8)
            key: str | int = name
            if name & 0x80000000:
                name_relative = name & 0x7FFFFFFF
                chars, = unpack("<H", resource_offset(name_relative, 2))
                if chars > 256:
                    raise ValueError("resource name is too long")
                start = resource_offset(name_relative + 2, chars * 2)
                key = data[start:start + chars * 2].decode("utf-16le")
            result.append((key, target))
        return result

    current = 0
    for name in ("INTEGRITY", "ELECTRONASAR"):
        targets = [target for key, target in directory(current) if key == name]
        if len(targets) != 1 or not targets[0] & 0x80000000:
            raise ValueError(f"expected one named {name} resource directory")
        current = targets[0] & 0x7FFFFFFF
    languages = directory(current)
    if len(languages) != 1 or languages[0][0] != 1033 or languages[0][1] & 0x80000000:
        raise ValueError("expected one English Electron integrity resource")
    payload_rva, payload_size, codepage, reserved = unpack("<IIII", resource_offset(languages[0][1], 16))
    if not 64 <= payload_size <= 4096 or reserved != 0:
        raise ValueError("invalid Electron integrity resource descriptor")
    payload_offset = file_offset(payload_rva, payload_size)
    if not base <= payload_offset or payload_offset + payload_size > base + resource_size:
        raise ValueError("integrity payload outside resource section")
    payload = data[payload_offset:payload_offset + payload_size]
    try:
        decoded = json.loads(payload)
    except (ValueError, UnicodeError) as error:
        raise ValueError("invalid Electron integrity JSON") from error
    if (not isinstance(decoded, list) or len(decoded) != 1 or
            not isinstance(decoded[0], dict) or set(decoded[0]) != {"file", "alg", "value"} or
            decoded[0]["file"] != "resources\\app.asar" or decoded[0]["alg"] != "SHA256" or
            not isinstance(decoded[0]["value"], str) or
            re.fullmatch(r"[0-9a-f]{64}", decoded[0]["value"]) is None):
        raise ValueError("unexpected Electron integrity resource contract")
    matches = list(re.finditer(rb'"value"\s*:\s*"([0-9a-f]{64})"', payload))
    if len(matches) != 1:
        raise ValueError("ambiguous integrity hash field")
    return payload_offset + matches[0].start(1), decoded[0]["value"]


def verify_desktop_integrity(original: Path, runtime: Path, archive: Path) -> dict[str, object]:
    verify_integrity_fuses(runtime.parent)
    source = _read_pe(original)
    actual = _read_pe(runtime)
    slot, original_hash = integrity_hash_slot(source)
    runtime_slot, runtime_hash = integrity_hash_slot(actual)
    expected_hash = asar_header_sha256(archive)
    expected = source[:slot] + expected_hash.encode("ascii") + source[slot + 64:]
    if runtime_slot != slot or runtime_hash != expected_hash or actual != expected:
        raise ValueError("desktop differs outside the authorized ASAR integrity hash or has the wrong hash")
    return {
        "originalDesktopSha256": hashlib.sha256(source).hexdigest(),
        "runtimeDesktopSha256": hashlib.sha256(actual).hexdigest(),
        "originalAsarHeaderSha256": original_hash,
        "asarHeaderSha256": expected_hash,
        "resourcePath": "INTEGRITY/ELECTRONASAR/1033",
        "signatureStatus": "locally-modified-original-preserved",
        "integrityEnabled": True,
    }


def patch_desktop_integrity(original: Path, runtime: Path, archive: Path, source_archive: Path) -> dict[str, object]:
    if original.resolve() == runtime.resolve() or original.samefile(runtime):
        raise ValueError("original executable must be preserved separately")
    source = _read_pe(original)
    slot, original_hash = integrity_hash_slot(source)
    if original_hash != asar_header_sha256(source_archive):
        raise ValueError("original executable integrity does not match the official ASAR")
    if _read_pe(runtime) != source:
        raise ValueError("runtime must initially be an exact copy of the signed original")
    verify_integrity_fuses(runtime.parent)
    digest = asar_header_sha256(archive).encode("ascii")
    runtime.write_bytes(source[:slot] + digest + source[slot + 64:])
    return verify_desktop_integrity(original, runtime, archive)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", nargs=3, type=Path, metavar=("ORIGINAL", "RUNTIME", "ASAR"), required=True)
    args = parser.parse_args()
    print(json.dumps(verify_desktop_integrity(*args.verify)))

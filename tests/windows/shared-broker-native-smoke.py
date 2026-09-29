#!/usr/bin/env python3
"""Isolated no-inference smoke for the shared native app-server broker.

The mux and exact native CLI are supplied explicitly. All homes, router state,
usage data, SQLite data, and a synthetic project root live under one temporary
directory. The broker descriptor and RPC token are read only in memory and are
never printed or copied to retained artifacts.

This smoke sends initialization, list/read, project create/update, thread start
(which creates an empty chat only), and thread rename. It never sends turn/start,
login, credential, reset, approval, prompt, or inference requests.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import uuid
from typing import Any


PROTOCOL = 1
MAX_DESCRIPTOR_BYTES = 16 * 1024


class SmokeFailure(RuntimeError):
    pass


class RPCClient:
    def __init__(self, address: str, token: str, timeout: float):
        host, port_text = address.rsplit(":", 1)
        if host != "127.0.0.1":
            raise SmokeFailure("broker RPC address is not IPv4 loopback")
        port = int(port_text)
        if not 1 <= port <= 65535:
            raise SmokeFailure("broker RPC port is invalid")
        self.timeout = timeout
        self.sock = socket.create_connection((host, port), timeout=timeout)
        self.sock.settimeout(timeout)
        self.reader = self.sock.makefile("rb")
        self.next_id = 0
        hello = json.dumps({"protocol": PROTOCOL, "token": token}, separators=(",", ":")).encode() + b"\n"
        self.sock.sendall(hello)
        if self.reader.readline(MAX_DESCRIPTOR_BYTES) != b'{"ok":true}\n':
            self.close()
            raise SmokeFailure("broker RPC handshake failed")

    def close(self) -> None:
        try:
            self.reader.close()
        except Exception:
            pass
        try:
            self.sock.close()
        except Exception:
            pass

    def notify(self, method: str, params: dict[str, Any] | None = None) -> None:
        message: dict[str, Any] = {"method": method}
        if params is not None:
            message["params"] = params
        self.sock.sendall(json.dumps(message, separators=(",", ":")).encode() + b"\n")

    def request(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        self.next_id += 1
        request_id = self.next_id
        message: dict[str, Any] = {"id": request_id, "method": method, "params": params or {}}
        self.sock.sendall(json.dumps(message, separators=(",", ":")).encode() + b"\n")
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            self.sock.settimeout(max(0.1, deadline - time.monotonic()))
            try:
                line = self.reader.readline(64 * 1024 * 1024)
            except (TimeoutError, socket.timeout):
                break
            if not line:
                break
            try:
                response = json.loads(line)
            except (json.JSONDecodeError, UnicodeDecodeError):
                continue
            if isinstance(response, dict) and response.get("id") == request_id:
                return response
        raise SmokeFailure(f"RPC timed out or connection closed for {method}")


def result_for(client: RPCClient, method: str, params: dict[str, Any] | None = None) -> Any:
    response = client.request(method, params)
    error = response.get("error")
    if error is not None:
        code = error.get("code") if isinstance(error, dict) else "unknown"
        raise SmokeFailure(f"RPC {method} returned error code {code}")
    if "result" not in response:
        raise SmokeFailure(f"RPC {method} returned no result")
    return response["result"]


def require_ok(client: RPCClient, method: str, params: dict[str, Any] | None = None) -> Any:
    return result_for(client, method, params)


def init_client(client: RPCClient, experimental_api: bool = True, notify_initialized: bool = True) -> None:
    response = client.request(
        "initialize",
        {
            "clientInfo": {"name": "csr_shared_broker_native_smoke", "version": "1"},
            "capabilities": {"experimentalApi": experimental_api},
        },
    )
    error = response.get("error")
    if error is not None:
        code = error.get("code") if isinstance(error, dict) else "unknown"
        raise SmokeFailure(f"initialize returned error code {code}")
    if not isinstance(response.get("result"), dict):
        raise SmokeFailure("initialize returned a non-object result")
    if notify_initialized:
        client.notify("initialized")


def data_array(value: Any, method: str) -> list[Any]:
    if not isinstance(value, dict) or not isinstance(value.get("data"), list):
        raise SmokeFailure(f"{method} response did not contain a data array")
    return value["data"]


def safe_shape(value: Any, depth: int = 0) -> Any:
    """Describe structure only; string values and payloads are never emitted."""
    if depth >= 4:
        return "..."
    if isinstance(value, dict):
        return {str(key): safe_shape(child, depth + 1) for key, child in list(value.items())[:24]}
    if isinstance(value, list):
        return {"type": "list", "count": len(value), "first": safe_shape(value[0], depth + 1) if value else None}
    if isinstance(value, str):
        return "<string>"
    if value is None or isinstance(value, (bool, int, float)):
        return type(value).__name__
    return type(value).__name__


def find_entry_by_name(entries: list[Any], name: str, kind: str) -> dict[str, Any]:
    pending = list(entries)
    while pending:
        entry = pending.pop(0)
        if isinstance(entry, dict):
            if entry.get("name") == name or entry.get("title") == name:
                return entry
            # Native list rows can wrap the resource under a stable key or
            # nest its display metadata one level deeper. Walk only the
            # in-memory decoded object; never print its contents.
            pending.extend(entry.values())
        elif isinstance(entry, list):
            pending.extend(entry)
    raise SmokeFailure(f"{kind} list did not show the synthetic renamed item")


def find_identifier(result: Any, names: tuple[str, ...]) -> str | None:
    if isinstance(result, dict):
        for name in names:
            value = result.get(name)
            if isinstance(value, str) and value:
                return value
        # Prefer the method's named resource before recursively looking at
        # nested wrappers that may contain unrelated IDs.
        for preferred in ("project", "thread"):
            nested = result.get(preferred)
            if isinstance(nested, dict):
                found = find_identifier(nested, ("id", *names))
                if found:
                    return found
        for value in result.values():
            found = find_identifier(value, names)
            if found:
                return found
    elif isinstance(result, list):
        for value in result:
            found = find_identifier(value, names)
            if found:
                return found
    return None


def find_dict_by_value(root: Any, key: str, expected: str) -> dict[str, Any] | None:
    pending = [root]
    while pending:
        value = pending.pop(0)
        if isinstance(value, dict):
            if value.get(key) == expected:
                return value
            pending.extend(value.values())
        elif isinstance(value, list):
            pending.extend(value)
    return None


def read_descriptor(path: Path) -> dict[str, Any]:
    try:
        info = path.lstat()
    except OSError as exc:
        raise SmokeFailure("broker descriptor was not created") from exc
    if not path.is_file() or path.is_symlink() or info.st_size <= 0 or info.st_size > MAX_DESCRIPTOR_BYTES:
        raise SmokeFailure("broker descriptor is not a bounded regular file")
    try:
        descriptor = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise SmokeFailure("broker descriptor is invalid") from exc
    if descriptor.get("protocol") != PROTOCOL:
        raise SmokeFailure("broker descriptor protocol did not match")
    token = descriptor.get("token")
    if not isinstance(token, str) or not re.fullmatch(r"[0-9a-f]{64}", token):
        raise SmokeFailure("broker descriptor authentication field is invalid")
    binding = descriptor.get("binding")
    if not isinstance(binding, dict):
        raise SmokeFailure("broker descriptor binding is invalid")
    return descriptor


def wait_for_descriptor(process: subprocess.Popen[bytes], descriptor_path: Path, timeout: float) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise SmokeFailure("broker exited before publishing its session descriptor")
        if descriptor_path.exists():
            return read_descriptor(descriptor_path)
        time.sleep(0.1)
    raise SmokeFailure("broker did not publish its session descriptor before timeout")


def stop_process_tree(process: subprocess.Popen[bytes], grace: float) -> bool:
    """Stop only this test's process tree; return whether cleanup was forced."""
    if process.poll() is not None:
        return False
    forced = False
    if os.name == "nt":
        try:
            process.send_signal(signal.CTRL_BREAK_EVENT)
        except (OSError, ValueError):
            pass
    else:
        try:
            process.send_signal(signal.SIGINT)
        except OSError:
            pass
    try:
        process.wait(timeout=grace)
    except subprocess.TimeoutExpired:
        forced = True
        # The PID is the broker process created by this script. /T confines
        # forced cleanup to its descendants if graceful shutdown failed.
        if os.name == "nt" and process.poll() is None:
            try:
                subprocess.run(
                    ["taskkill", "/PID", str(process.pid), "/T", "/F"],
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=10,
                    check=False,
                )
            except (OSError, subprocess.TimeoutExpired):
                pass
        if process.poll() is None:
            process.kill()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            return True
    return forced


def validate_file_argument(path_text: str, label: str) -> Path:
    path = Path(path_text).expanduser().resolve(strict=True)
    if not path.is_file() or path.is_symlink():
        raise SmokeFailure(f"{label} must be a regular executable file")
    return path


def run_smoke(args: argparse.Namespace) -> dict[str, Any]:
    if os.name != "nt":
        raise SmokeFailure("this shared-broker smoke requires Windows")
    mux_path = validate_file_argument(args.mux, "mux")
    native_path = validate_file_argument(args.native, "native CLI")
    if mux_path == native_path:
        raise SmokeFailure("mux and native CLI must be different executables")

    with tempfile.TemporaryDirectory(prefix="csr-shared-broker-smoke-") as temp_text:
        temporary_root = Path(temp_text).resolve()
        paths = {
            "state": temporary_root / "state",
            "usage": temporary_root / "usage",
            "home": temporary_root / "home",
            "sqlite": temporary_root / "sqlite",
            "profile": temporary_root / "profile",
            "project": temporary_root / "synthetic-project",
            "appdata": temporary_root / "appdata",
            "localappdata": temporary_root / "localappdata",
        }
        for value in paths.values():
            value.mkdir(parents=True, exist_ok=True)
        (paths["project"] / ".csr-smoke-marker").write_text("synthetic-only\n", encoding="utf-8")
        descriptor_path = paths["state"] / "broker-session.json"

        # Preserve only process plumbing needed by the native binary. All
        # profile-bearing and credential-bearing variables are dropped.
        env = {key: os.environ[key] for key in ("PATH", "SystemRoot", "WINDIR", "ComSpec") if key in os.environ}
        env.update(
            {
                "CODEX_MUX_SHARED_ROOT": str(paths["state"]),
                "CODEX_MUX_SHARED_PROTOCOL": str(PROTOCOL),
                "SHARED_PROTOCOL": str(PROTOCOL),
                "CODEX_MUX_USAGE_ROOT": str(paths["usage"]),
                "CODEX_MUX_REAL_CODEX": str(native_path),
                "CODEX_HOME": str(paths["home"]),
                "CODEX_SQLITE_HOME": str(paths["sqlite"]),
                "CODEX_MUX_HOME": str(paths["state"]),
                "USERPROFILE": str(paths["profile"]),
                "HOME": str(paths["home"]),
                "APPDATA": str(paths["appdata"]),
                "LOCALAPPDATA": str(paths["localappdata"]),
                "TEMP": str(temporary_root),
                "TMP": str(temporary_root),
            }
        )

        process: subprocess.Popen[bytes] | None = None
        clients: list[RPCClient] = []
        summary: dict[str, Any] = {
            "protocol": PROTOCOL,
            "initializedClients": 0,
            "initialThreadCount": None,
            "initialProjectCount": None,
            "syntheticProjectVisibleCrossClient": False,
            "syntheticThreadVisibleCrossClient": False,
            "projectRenameVisibleCrossClient": False,
            "threadRenameVisibleCrossClient": False,
            "accountReadConsistent": False,
            "clientIsolationAfterClose": False,
            "incompatibleInitializeRejected": False,
            "secondBrokerRejected": False,
            "inferenceRequestsSent": 0,
        }

        try:
            process = subprocess.Popen(
                [str(mux_path), "--router-broker", "app-server"],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                env=env,
                cwd=str(temporary_root),
                creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
            )
            descriptor = wait_for_descriptor(process, descriptor_path, args.timeout)
            address = descriptor.get("rpcAddress")
            token = descriptor["token"]
            if not isinstance(address, str):
                raise SmokeFailure("broker RPC address was missing")

            first = RPCClient(address, token, args.timeout)
            clients.append(first)
            init_client(first, experimental_api=True, notify_initialized=not args.implicit_initialized)
            summary["initializedClients"] += 1

            # A second broker must not own or replace the active shared state.
            contender = subprocess.Popen(
                [str(mux_path), "--router-broker", "app-server"],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                env=env,
                cwd=str(temporary_root),
                creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
            )
            try:
                contender.wait(timeout=min(10.0, args.timeout))
            except subprocess.TimeoutExpired as exc:
                stop_process_tree(contender, grace=1.0)
                raise SmokeFailure("a second broker did not fail promptly on the shared-root lock") from exc
            if contender.returncode == 0:
                raise SmokeFailure("a second broker unexpectedly acquired the shared-root lock")
            summary["secondBrokerRejected"] = True

            second = RPCClient(address, token, args.timeout)
            clients.append(second)
            init_client(second, experimental_api=True, notify_initialized=not args.implicit_initialized)
            summary["initializedClients"] += 1

            threads_before = data_array(require_ok(first, "thread/list", {"limit": 10, "useStateDbOnly": True}), "thread/list")
            projects_before = data_array(require_ok(first, "project/list", {"limit": 10}), "project/list")
            summary["initialThreadCount"] = len(threads_before)
            summary["initialProjectCount"] = len(projects_before)
            if threads_before or projects_before:
                raise SmokeFailure("isolated CODEX_HOME unexpectedly contained prior projects or threads")

            account_first = require_ok(first, "account/read", {})
            account_second = require_ok(second, "account/read", {})
            if not isinstance(account_first, dict) or account_first != account_second:
                raise SmokeFailure("account/read metadata differed between broker clients")
            if account_first.get("account", None) is not None:
                raise SmokeFailure("isolated account/read unexpectedly reported an authenticated account")
            summary["accountReadConsistent"] = True

            project_name = "CSR isolated broker smoke " + uuid.uuid4().hex[:10]
            project_create = require_ok(
                first,
                "project/create",
                {
                    "idempotencyKey": str(uuid.uuid4()),
                    "name": project_name,
                    "roots": [{"path": str(paths["project"])}],
                    "metadata": {"source": "synthetic-no-inference-smoke"},
                },
            )
            project_id = find_identifier(project_create, ("projectId", "id"))
            if not project_id:
                project_row = find_entry_by_name(data_array(require_ok(second, "project/list", {"limit": 20}), "project/list"), project_name, "project")
                project_id = project_row.get("id") if isinstance(project_row.get("id"), str) else None
            if not project_id:
                raise SmokeFailure("project/create did not yield a usable synthetic project identity")
            project_row_second = find_entry_by_name(data_array(require_ok(second, "project/list", {"limit": 20}), "project/list"), project_name, "project")
            if project_row_second.get("id") != project_id:
                raise SmokeFailure("second client did not observe the created synthetic project")
            summary["syntheticProjectVisibleCrossClient"] = True

            project_renamed = project_name + " renamed"
            require_ok(second, "project/update", {"projectId": project_id, "name": project_renamed})
            project_row_first = find_entry_by_name(data_array(require_ok(first, "project/list", {"limit": 20}), "project/list"), project_renamed, "project")
            if project_row_first.get("id") != project_id:
                raise SmokeFailure("first client did not observe the cross-client project rename")
            summary["projectRenameVisibleCrossClient"] = True

            thread_name = "CSR isolated chat " + uuid.uuid4().hex[:10]
            thread_start = require_ok(
                first,
                "thread/start",
                {
                    "cwd": str(paths["project"]),
                    "projectId": project_id,
                    "ephemeral": False,
                },
            )
            thread_id = find_identifier(thread_start, ("threadId", "id"))
            if not thread_id:
                visible = data_array(require_ok(second, "thread/list", {"limit": 20, "useStateDbOnly": True}), "thread/list")
                candidates = [item for item in visible if isinstance(item, dict)]
                if len(candidates) == 1:
                    thread_id = candidates[0].get("id")
            if not isinstance(thread_id, str) or not thread_id:
                raise SmokeFailure("thread/start did not yield a synthetic empty-chat identity")
            # An empty thread has no turns and may not be indexed into
            # thread/list until its first turn. Use the schema's metadata-only
            # thread/read operation to verify visibility without inference.
            before_rename = require_ok(second, "thread/read", {"threadId": thread_id, "includeTurns": False})
            visible_thread = find_dict_by_value(before_rename, "id", thread_id)
            if visible_thread is None:
                raise SmokeFailure("second client could not read the synthetic empty chat")
            require_ok(second, "thread/name/set", {"threadId": thread_id, "name": thread_name})
            after_rename = require_ok(first, "thread/read", {"threadId": thread_id, "includeTurns": False})
            renamed_thread = find_dict_by_value(after_rename, "id", thread_id)
            if renamed_thread is None or renamed_thread.get("name") != thread_name:
                raise SmokeFailure(f"first client did not observe cross-client thread rename; read-shape={safe_shape(after_rename)}")
            summary["syntheticThreadVisibleCrossClient"] = True
            summary["threadRenameVisibleCrossClient"] = True

            # An incompatible capabilities fingerprint is rejected without
            # changing or disconnecting the compatible shared clients.
            mismatch = RPCClient(address, token, args.timeout)
            clients.append(mismatch)
            mismatch_response = mismatch.request(
                "initialize",
                {
                    "clientInfo": {"name": "csr_shared_broker_native_smoke", "version": "1"},
                    "capabilities": {"experimentalApi": False},
                },
            )
            error = mismatch_response.get("error")
            summary["incompatibleInitializeRejected"] = isinstance(error, dict) and error.get("code") == -32081
            if not summary["incompatibleInitializeRejected"]:
                raise SmokeFailure("broker did not reject a client with incompatible initialize capabilities")
            mismatch.close()
            clients.remove(mismatch)

            # Closing one desktop must not close the broker or the other client.
            first.close()
            clients.remove(first)
            account_after_close = require_ok(second, "account/read", {})
            project_after_close = data_array(require_ok(second, "project/list", {"limit": 20}), "project/list")
            find_entry_by_name(project_after_close, project_renamed, "project")
            if account_after_close != account_second:
                raise SmokeFailure("remaining client lost account metadata after its peer disconnected")
            if process.poll() is not None:
                raise SmokeFailure("broker exited when only one desktop client closed")
            summary["clientIsolationAfterClose"] = True
            return summary
        finally:
            for client in reversed(clients):
                client.close()
            if process is not None:
                forced = stop_process_tree(process, grace=args.shutdown_timeout)
                if forced:
                    summary["forcedCleanup"] = True
                else:
                    summary["forcedCleanup"] = False


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mux", required=True, help="new codex-mux.exe build")
    parser.add_argument("--native", required=True, help="isolated native Codex CLI executable (26.924)")
    parser.add_argument("--timeout", type=float, default=30.0, help="per-RPC/startup timeout in seconds")
    parser.add_argument("--implicit-initialized", action="store_true", help="match native desktop: requests follow initialize success without a notification")
    parser.add_argument("--shutdown-timeout", type=float, default=10.0, help="graceful broker shutdown timeout")
    parser.add_argument("--keep-artifacts", action="store_true", help="write only a sanitized JSON summary in this directory")
    parser.add_argument("--artifacts-dir", help="destination for sanitized JSON summary with --keep-artifacts")
    args = parser.parse_args()
    if args.timeout < 5 or args.timeout > 120 or args.shutdown_timeout < 1 or args.shutdown_timeout > 60:
        parser.error("timeouts are outside the supported bounds")
    if args.keep_artifacts and not args.artifacts_dir:
        parser.error("--keep-artifacts requires --artifacts-dir")

    try:
        summary = run_smoke(args)
    except SmokeFailure as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    except Exception:
        # Do not dump RPC payloads, subprocess environment, descriptor values,
        # or exception-local socket/credential material in an unfiltered trace.
        print("FAIL: unexpected smoke failure (details suppressed to protect isolated descriptor data)", file=sys.stderr)
        return 1

    if args.keep_artifacts:
        output_dir = Path(args.artifacts_dir).expanduser().resolve()
        output_dir.mkdir(parents=True, exist_ok=True)
        target = output_dir / "shared-broker-native-smoke-summary.json"
        target.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print("PASS: shared broker native smoke; 2 compatible clients, isolated metadata, cross-client project/chat updates, peer-close continuity, incompatible initialize and duplicate broker lock rejected; 0 inference requests")
    if args.keep_artifacts:
        print("PASS: sanitized summary saved (no descriptor, token, RPC bodies, credentials, or user content)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

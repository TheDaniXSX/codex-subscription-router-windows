from __future__ import annotations

import contextlib
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
PATCHER_PATH = REPOSITORY_ROOT / "scripts" / "patch_windows_app.py"
SPEC = importlib.util.spec_from_file_location("patch_windows_app_lifecycle", PATCHER_PATH)
assert SPEC is not None and SPEC.loader is not None
patcher = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = patcher
SPEC.loader.exec_module(patcher)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def deterministic_tree_digest(root: Path) -> str:
    """Match the inventory/verifier contract used in build manifests."""
    records = []
    for path in root.rglob("*"):
        if path.is_file():
            relative = path.relative_to(root).as_posix()
            records.append((relative.lower(), relative, sha256(path)))
    records.sort(key=lambda item: item[0])
    payload = "".join(f"{relative}\0{digest}\n" for _, relative, digest in records)
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def tree_snapshot(root: Path, *, ignore_logs: bool = False) -> dict[str, tuple[str, str]]:
    if not root.exists():
        return {}
    result: dict[str, tuple[str, str]] = {}
    for path in sorted(root.rglob("*"), key=lambda item: str(item).casefold()):
        relative = path.relative_to(root).as_posix()
        if ignore_logs and ("/logs/" in f"/{relative}/" or relative.endswith("/logs")):
            continue
        if path.is_symlink():
            result[relative] = ("link", os.readlink(path))
        elif path.is_dir():
            result[relative] = ("dir", "")
        else:
            result[relative] = ("file", sha256(path))
    return result


class PatcherLifecycleTests(unittest.TestCase):
    """Exercises install/update transactions without official binaries or credentials."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="codex-router-lifecycle-patcher-")
        self.root = Path(self.temporary.name)
        self.allowed = self.root / "allowed"
        self.allowed.mkdir()
        self.outside = self.root / "outside"
        self.outside.mkdir()
        (self.outside / "sentinel.txt").write_text("outside", encoding="utf-8")
        self.source = self.allowed / "source"
        self.source.mkdir()
        self.destination = self.allowed / "apps" / "Router"
        self.destination.parent.mkdir(parents=True)
        self.state = self.allowed / "state"

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _fake_manifest(
        self,
        staged: Path,
        _source: object,
        _destination: Path,
        _state: Path,
        _mux: Path,
        _launcher: Path,
        _preservation: object,
        backup_path: Path | None = None,
        control_port: int | None = None,
    ) -> dict[str, object]:
        manifest: dict[str, object] = {
            "schemaVersion": 1,
            "destination": str(self.destination),
            "profilePath": str(self.state / "Profile"),
            "build": "fixture-new",
            "backupPath": str(backup_path) if backup_path is not None else None,
            "controlPort": control_port,
        }
        (staged / "codex-mux-build.json").write_text(
            json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
        )
        return manifest

    @contextlib.contextmanager
    def _mock_build(self):
        source = SimpleNamespace(
            app_root=self.source,
            package_full_name="OpenAI.Codex_fixture",
            asar_version="fixture",
            asar_build="fixture",
            asar_sha256="0" * 64,
        )

        def fake_prepare(_provided: object, output: Path, **_kwargs: object) -> Path:
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_bytes(b"MZfixture")
            return output

        def fake_copytree(_source: Path, staged: Path, **_kwargs: object) -> Path:
            (staged / "resources" / "app.asar.unpacked").mkdir(parents=True)
            (staged / "resources" / "app.asar").write_bytes(b"fixture-asar")
            return staged

        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(patcher, "inspect_source", return_value=source))
            stack.enter_context(mock.patch.object(patcher, "validate_source_destination"))
            stack.enter_context(mock.patch.object(patcher, "validate_approved_source"))
            stack.enter_context(mock.patch.object(patcher, "ensure_asar_tool", return_value=Path("asar.cmd")))
            stack.enter_context(mock.patch.object(patcher, "prepare_control_token", return_value=("a" * 64, False)))
            stack.enter_context(mock.patch.object(patcher, "prepare_executable", side_effect=fake_prepare))
            stack.enter_context(mock.patch.object(patcher.shutil, "copytree", side_effect=fake_copytree))
            stack.enter_context(mock.patch.object(patcher, "patch_owl_config"))
            stack.enter_context(mock.patch.object(patcher, "run"))
            stack.enter_context(mock.patch.object(patcher, "patch_extracted_asar"))
            stack.enter_context(mock.patch.object(patcher, "repack_asar", return_value=()))
            stack.enter_context(mock.patch.object(patcher, "install_repacked_asar"))
            stack.enter_context(mock.patch.object(patcher, "swap_executables"))
            stack.enter_context(mock.patch.object(patcher, "verify_preserved_windows_resources", return_value={}))
            stack.enter_context(mock.patch.object(patcher, "write_build_manifest", side_effect=self._fake_manifest))
            stack.enter_context(mock.patch.dict(os.environ, {"CODEX_ROUTER_DATA_DIR": str(self.state)}))
            yield

    def _run_patch(self, *, dry_run: bool, force: bool) -> dict[str, object]:
        with open(os.devnull, "w", encoding="utf-8") as sink:
            with self._mock_build(), contextlib.redirect_stdout(sink):
                return patcher.patch_app(
                    self.source,
                    self.destination,
                    None,
                    None,
                    force=force,
                    dry_run=dry_run,
                    allow_untested_source=True,
                )

    def test_install_dry_run_changes_no_destination_state_or_guard(self) -> None:
        before = tree_snapshot(self.root)
        manifest = self._run_patch(dry_run=True, force=False)
        self.assertTrue(manifest["dryRun"])
        self.assertEqual(tree_snapshot(self.root), before)
        self.assertFalse(self.destination.exists())
        self.assertFalse(self.state.exists())

    def test_fresh_install_persists_explicit_null_backup_path(self) -> None:
        outside_before = tree_snapshot(self.outside)
        returned = self._run_patch(dry_run=False, force=False)
        stored = json.loads((self.destination / "codex-mux-build.json").read_text(encoding="utf-8"))
        self.assertIn("backupPath", stored)
        self.assertIsNone(stored["backupPath"])
        self.assertIsNone(returned["backupPath"])
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_update_persists_recoverable_backup_path(self) -> None:
        self.destination.mkdir()
        (self.destination / "old.txt").write_text("old-build", encoding="utf-8")
        outside_before = tree_snapshot(self.outside)
        returned = self._run_patch(dry_run=False, force=True)
        stored = json.loads((self.destination / "codex-mux-build.json").read_text(encoding="utf-8"))
        backup = Path(str(stored["backupPath"]))
        self.assertEqual(backup, Path(str(returned["backupPath"])))
        self.assertTrue((backup / "old.txt").is_file())
        self.assertEqual((backup / "old.txt").read_text(encoding="utf-8"), "old-build")
        self.assertTrue(backup.resolve().is_relative_to(self.allowed.resolve()))
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_update_commit_failure_restores_old_build_and_stays_in_fixture(self) -> None:
        staged = self.allowed / "staged"
        staged.mkdir()
        (staged / "new.txt").write_text("new-build", encoding="utf-8")
        self.destination.mkdir()
        (self.destination / "old.txt").write_text("old-build", encoding="utf-8")
        outside_before = tree_snapshot(self.outside)
        original_rename = Path.rename

        def failing_rename(path: Path, target: Path) -> Path:
            if path == staged and target == self.destination:
                raise OSError("injected commit failure")
            return original_rename(path, target)

        with mock.patch.object(Path, "rename", autospec=True, side_effect=failing_rename):
            with self.assertRaisesRegex(OSError, "injected commit failure"):
                patcher.atomic_install(staged, self.destination, force=True)
        self.assertEqual((self.destination / "old.txt").read_text(encoding="utf-8"), "old-build")
        self.assertTrue((staged / "new.txt").is_file())
        self.assertEqual(tree_snapshot(self.outside), outside_before)


@unittest.skipUnless(os.name == "nt", "Windows lifecycle scripts require Windows")
class PowerShellLifecycleTests(unittest.TestCase):
    """Runs real rollback/uninstall/cleanup scripts only against disposable fixtures."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.shell = shutil.which("pwsh") or shutil.which("powershell")
        if cls.shell is None:
            raise unittest.SkipTest("PowerShell is unavailable")

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="codex-router-lifecycle-")
        self.root = Path(self.temporary.name)
        self.allowed = self.root / "allowed"
        self.allowed.mkdir()
        self.outside = self.root / "outside"
        self.outside.mkdir()
        (self.outside / "sentinel.txt").write_text("must-not-change", encoding="utf-8")
        self.tool = self.allowed / "tool"
        self.tool.mkdir()
        for name in (
            "WindowsLifecycle.psm1",
            "rollback_windows.ps1",
            "uninstall_windows.ps1",
            "cleanup_windows.ps1",
        ):
            shutil.copy2(REPOSITORY_ROOT / "scripts" / name, self.tool / name)
        self.fake_shell_integration = self.tool / "fake-shell-integration.ps1"
        self.fake_shell_integration.write_text(
            "[CmdletBinding(SupportsShouldProcess=$true)]\n"
            "param([string]$Action,[string]$Feature,[string]$LauncherPath)\n"
            "Write-Host ('FixtureShellIntegration: ' + $Action + ' ' + $Feature)\n",
            encoding="utf-8",
        )
        self.destination = self.allowed / "apps" / "Router"
        self.state = self.allowed / "state"
        self.backups = self.allowed / "backups"
        self.shortcut = self.allowed / "start" / "Codex Subscription Router.lnk"
        self.env = os.environ.copy()
        self.env.update(
            {
                "LOCALAPPDATA": str(self.allowed / "env" / "local"),
                "APPDATA": str(self.allowed / "env" / "roaming"),
                # AllowedRoot must be beneath the subprocess TEMP root before
                # the hidden, fixture-only failure injector is accepted.
                "TEMP": str(self.root),
                "TMP": str(self.root),
                "USERPROFILE": str(self.allowed / "env" / "profile"),
                "HOME": str(self.allowed / "env" / "profile"),
                "POWERSHELL_TELEMETRY_OPTOUT": "1",
            }
        )
        for key in ("LOCALAPPDATA", "APPDATA", "TEMP", "USERPROFILE"):
            Path(self.env[key]).mkdir(parents=True, exist_ok=True)
        self.work = self.allowed / "work"
        self.work.mkdir()
        # PowerShell can initialize its own per-user caches on first startup.
        # Warm those caches before any no-mutation snapshot is captured.
        subprocess.run(
            [str(self.shell), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "exit 0"],
            cwd=self.work,
            env=self.env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=15,
            check=True,
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _common_arguments(self) -> dict[str, object]:
        return {
            "Destination": self.destination,
            "StateRoot": self.state,
            "BackupRoot": self.backups,
            "AllowedRoot": self.allowed,
        }

    def _operation_snapshot(self) -> dict[str, object]:
        """Snapshot every managed target plus the explicit outside guard.

        PowerShell itself rewrites a per-user StartupProfileData cache on every
        process start, even with -NoProfile. That shell-owned file lives in the
        fixture environment and is deliberately excluded; all lifecycle inputs,
        the shortcut, and the outside sentinel remain covered byte-for-byte.
        """
        shortcut = (
            ("file", sha256(self.shortcut))
            if self.shortcut.is_file()
            else ("missing", "")
        )
        return {
            "destination": tree_snapshot(self.destination),
            "state": tree_snapshot(self.state),
            "backups": tree_snapshot(self.backups),
            "shortcut": shortcut,
            "outside": tree_snapshot(self.outside),
        }

    def _run(
        self,
        script: str,
        *,
        expected: int = 0,
        arguments: dict[str, object] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        values = self._common_arguments()
        if arguments:
            values.update(arguments)
        command = [
            str(self.shell),
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(self.tool / script),
        ]
        for name, value in values.items():
            if value is None:
                continue
            if value is False:
                command.append(f"-{name}:$false")
                continue
            command.append(f"-{name}")
            if value is not True:
                command.append(str(value))
        result = subprocess.run(
            command,
            cwd=self.work,
            env=self.env,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=30,
            check=False,
        )
        if result.returncode != expected:
            self.fail(
                f"{script} returned {result.returncode}, expected {expected}.\n"
                f"Command: {subprocess.list2cmdline(command)}\nOutput:\n{result.stdout}"
            )
        return result

    def _write_layout(
        self,
        path: Path,
        version: str,
        *,
        backup_path: Path | None = None,
    ) -> dict[str, object]:
        resources = path / "resources"
        resources.mkdir(parents=True)
        files = {
            "patchedAsarSha256": resources / "app.asar",
            "muxSha256": resources / "codex.exe",
            "launcherSha256": path / "ChatGPT.exe",
        }
        for label, file_path in files.items():
            file_path.write_bytes(f"{version}:{label}".encode("utf-8"))
        manifest: dict[str, object] = {
            "schemaVersion": 1,
            "destination": str(self.destination),
            "profilePath": str(self.state / "Profile"),
            "patchedAsarSha256": sha256(files["patchedAsarSha256"]),
            "muxSha256": sha256(files["muxSha256"]),
            "launcherSha256": sha256(files["launcherSha256"]),
            "fixtureVersion": version,
            "backupPath": str(backup_path) if backup_path else None,
        }
        (path / "codex-mux-build.json").write_text(
            json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
        )
        return manifest

    def _write_backup(self, name: str, version: str, age_seconds: int) -> tuple[Path, Path]:
        container = self.backups / name
        payload = container / self.destination.name
        self._write_layout(payload, version)
        timestamp = time.time() - age_seconds
        os.utime(container, (timestamp, timestamp))
        return container, payload

    def _make_shortcut(self, shortcut: Path, target: Path) -> None:
        shortcut.parent.mkdir(parents=True, exist_ok=True)
        helper = self.tool / "make-shortcut.ps1"
        helper.write_text(
            "param([string]$Path,[string]$Target)\n"
            "$shell=New-Object -ComObject WScript.Shell\n"
            "$link=$shell.CreateShortcut($Path)\n"
            "$link.TargetPath=$Target\n"
            "$link.WorkingDirectory=Split-Path -Parent $Target\n"
            "$link.Save()\n",
            encoding="utf-8",
        )
        result = subprocess.run(
            [str(self.shell), "-NoProfile", "-NonInteractive", "-File", str(helper), "-Path", str(shortcut), "-Target", str(target)],
            cwd=self.work,
            env=self.env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=15,
            check=False,
        )
        if result.returncode != 0:
            self.fail(f"Could not create fixture shortcut: {result.stdout}")

    def test_private_acl_recursively_allows_only_current_user_and_system(self) -> None:
        private_root = self.allowed / "private-acl"
        nested = private_root / "nested"
        nested.mkdir(parents=True)
        (nested / "embedded-token-payload.bin").write_bytes(b"fixture-not-a-real-token")
        command = (
            "Import-Module ($PSHOME + '\\Modules\\Microsoft.PowerShell.Security\\Microsoft.PowerShell.Security.psd1') -Force; "
            + "Import-Module '"
            + str(self.tool / "WindowsLifecycle.psm1").replace("'", "''")
            + "' -Force; "
            + "$admin=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator); "
            + "Write-Host ('is_admin=' + $admin); "
            + "Set-CsrPrivateDirectoryAcl -Path '"
            + str(private_root).replace("'", "''")
            + "'; [void](Assert-CsrPrivateDirectoryAcl -Path '"
            + str(private_root).replace("'", "''")
            + "')"
        )
        result = subprocess.run(
            [str(self.shell), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command],
            cwd=self.work,
            env=self.env,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=30,
            check=False,
        )
        if result.returncode != 0:
            self.fail(f"Private ACL fixture failed:\n{result.stdout}")
        self.assertIn("is_admin=", result.stdout)
        lifecycle_source = (self.tool / "WindowsLifecycle.psm1").read_text(encoding="utf-8")
        self.assertNotIn("Set-Acl -", lifecycle_source)
        self.assertNotIn("AuditRule", lifecycle_source)

    def test_tree_digest_matches_inventory_folded_ordinal_contract(self) -> None:
        tree = self.allowed / "tree-digest"
        first = tree / "bin" / "nodevars.bat"
        second = tree / "bin" / "node_modules" / ".bin" / "node-gyp-build"
        first.parent.mkdir(parents=True)
        second.parent.mkdir(parents=True)
        first.write_bytes(b"first")
        second.write_bytes(b"second")
        expected = deterministic_tree_digest(tree)
        command = (
            "Import-Module '"
            + str(self.tool / "WindowsLifecycle.psm1").replace("'", "''")
            + "' -Force; Get-CsrTreeDigest -Root '"
            + str(tree).replace("'", "''")
            + "'"
        )
        result = subprocess.run(
            [str(self.shell), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command],
            cwd=self.work,
            env=self.env,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=30,
            check=False,
        )
        if result.returncode != 0:
            self.fail(f"Tree digest fixture failed:\n{result.stdout}")
        self.assertEqual(result.stdout.strip(), expected)

    def test_private_acl_windows_powershell_51_non_elevated(self) -> None:
        desktop_shell = shutil.which("powershell.exe")
        if desktop_shell is None:
            self.skipTest("Windows PowerShell 5.1 is unavailable")
        private_root = self.allowed / "private-acl-ps51"
        nested = private_root / "nested"
        nested.mkdir(parents=True)
        (nested / "payload.bin").write_bytes(b"fixture")
        command = (
            "Import-Module ($PSHOME + '\\Modules\\Microsoft.PowerShell.Security\\Microsoft.PowerShell.Security.psd1') -Force; "
            + "Import-Module '"
            + str(self.tool / "WindowsLifecycle.psm1").replace("'", "''")
            + "' -Force; "
            + "$admin=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent()); "
            + "Write-Host ('is_admin=' + $admin.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)); "
            + "Set-CsrPrivateDirectoryAcl -Path '"
            + str(private_root).replace("'", "''")
            + "'; [void](Assert-CsrPrivateDirectoryAcl -Path '"
            + str(private_root).replace("'", "''")
            + "')"
        )
        result = subprocess.run(
            [desktop_shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command],
            cwd=self.work,
            env=self.env,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=30,
            check=False,
        )
        if "is_admin=True" in result.stdout:
            self.skipTest("Windows PowerShell test host is elevated")
        if result.returncode != 0:
            self.fail(f"Windows PowerShell 5.1 private ACL fixture failed:\n{result.stdout}")
        self.assertIn("is_admin=False", result.stdout)

    def test_rollback_whatif_is_a_complete_noop(self) -> None:
        _, backup = self._write_backup("20260101-older", "old", 20)
        self._write_layout(self.destination, "current", backup_path=backup)
        self.state.mkdir(parents=True)
        (self.state / "account-state.txt").write_text("preserve", encoding="utf-8")
        self._make_shortcut(self.shortcut, self.destination / "ChatGPT.exe")
        before = self._operation_snapshot()
        result = self._run(
            "rollback_windows.ps1",
            arguments={"BackupPath": backup, "ShortcutPath": self.shortcut, "WhatIf": True},
        )
        self.assertIn("WouldRollbackFrom:", result.stdout)
        self.assertEqual(self._operation_snapshot(), before)

    def test_rollback_uses_explicit_backup_and_preserves_state_and_shortcut(self) -> None:
        _, selected = self._write_backup("20260101-selected", "selected", 30)
        _, newer = self._write_backup("20260102-newer", "newer", 10)
        self._write_layout(self.destination, "current", backup_path=newer)
        self.state.mkdir(parents=True)
        state_file = self.state / "account-state.txt"
        state_file.write_text("preserve", encoding="utf-8")
        self._make_shortcut(self.shortcut, self.destination / "ChatGPT.exe")
        outside_before = tree_snapshot(self.outside)
        result = self._run(
            "rollback_windows.ps1",
            arguments={"BackupPath": selected.parent, "ShortcutPath": self.shortcut, "Confirm": False},
        )
        restored = json.loads((self.destination / "codex-mux-build.json").read_text(encoding="utf-8"))
        self.assertEqual(restored["fixtureVersion"], "selected")
        self.assertIn("BackupPath:", result.stdout)
        self.assertEqual(state_file.read_text(encoding="utf-8"), "preserve")
        self.assertTrue(self.shortcut.is_file())
        self.assertTrue(newer.is_dir())
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_injected_rollback_failure_restores_every_payload(self) -> None:
        _, backup = self._write_backup("20260101-backup", "old", 20)
        self._write_layout(self.destination, "current", backup_path=backup)
        self.state.mkdir(parents=True)
        active_before = tree_snapshot(self.destination)
        backup_before = tree_snapshot(backup)
        outside_before = tree_snapshot(self.outside)
        result = self._run(
            "rollback_windows.ps1",
            expected=1,
            arguments={"BackupPath": backup, "TestFailAfterMoveCurrent": True, "Confirm": False},
        )
        self.assertIn("pre-rollback installation was restored", result.stdout)
        self.assertIn("automatically.", result.stdout)
        self.assertEqual(tree_snapshot(self.destination), active_before)
        self.assertEqual(tree_snapshot(backup), backup_before)
        transactions = list(self.backups.glob("*-rollback-*"))
        self.assertEqual(transactions, [], f"rollback left transaction debris: {transactions}")
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_uninstall_whatif_preserves_app_state_backups_and_shortcut(self) -> None:
        _, backup = self._write_backup("20260101-backup", "old", 20)
        self._write_layout(self.destination, "current", backup_path=backup)
        self.state.mkdir(parents=True)
        (self.state / "account-state.txt").write_text("preserve", encoding="utf-8")
        self._make_shortcut(self.shortcut, self.destination / "ChatGPT.exe")
        before = self._operation_snapshot()
        self._run(
            "uninstall_windows.ps1",
            arguments={
                "ShortcutPath": self.shortcut,
                "ShortcutAllowedRoot": self.allowed,
                "ShellIntegrationScript": self.fake_shell_integration,
                "WhatIf": True,
            },
        )
        self.assertEqual(self._operation_snapshot(), before)

    def test_uninstall_preserves_state_and_backups_by_default(self) -> None:
        _, backup = self._write_backup("20260101-backup", "old", 20)
        self._write_layout(self.destination, "current", backup_path=backup)
        self.state.mkdir(parents=True)
        state_file = self.state / "account-state.txt"
        state_file.write_text("preserve", encoding="utf-8")
        self._make_shortcut(self.shortcut, self.destination / "ChatGPT.exe")
        outside_before = tree_snapshot(self.outside)
        self._run(
            "uninstall_windows.ps1",
            arguments={
                "ShortcutPath": self.shortcut,
                "ShortcutAllowedRoot": self.allowed,
                "ShellIntegrationScript": self.fake_shell_integration,
                "Confirm": False,
            },
        )
        self.assertFalse(self.destination.exists())
        self.assertFalse(self.shortcut.exists())
        self.assertEqual(state_file.read_text(encoding="utf-8"), "preserve")
        self.assertTrue(backup.is_dir())
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_uninstall_optionally_removes_state_and_authenticated_backups(self) -> None:
        self._write_backup("20260101-backup", "old", 20)
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        (self.state / "auth.json").write_text("fixture-secret", encoding="utf-8")
        self._make_shortcut(self.shortcut, self.destination / "ChatGPT.exe")
        outside_before = tree_snapshot(self.outside)
        self._run(
            "uninstall_windows.ps1",
            arguments={
                "ShortcutPath": self.shortcut,
                "ShortcutAllowedRoot": self.allowed,
                "ShellIntegrationScript": self.fake_shell_integration,
                "RemoveState": True,
                "RemoveBackups": True,
                "Confirm": False,
            },
        )
        self.assertFalse(self.destination.exists())
        self.assertFalse(self.state.exists())
        self.assertFalse(self.backups.exists())
        self.assertFalse(self.shortcut.exists())
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_uninstall_refuses_bulk_backup_removal_with_untrusted_content(self) -> None:
        self._write_backup("20260101-backup", "old", 20)
        rogue = self.backups / "untrusted"
        rogue.mkdir()
        (rogue / "do-not-delete.txt").write_text("untrusted", encoding="utf-8")
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        before = self._operation_snapshot()
        self._run(
            "uninstall_windows.ps1",
            expected=1,
            arguments={
                "ShortcutPath": None,
                "ShellIntegrationScript": self.fake_shell_integration,
                "RemoveBackups": True,
                "Confirm": False,
            },
        )
        self.assertEqual(self._operation_snapshot(), before)

    def test_uninstall_preserves_foreign_shortcut(self) -> None:
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        foreign_target = self.allowed / "foreign" / "Other.exe"
        foreign_target.parent.mkdir()
        foreign_target.write_bytes(b"MZforeign")
        self._make_shortcut(self.shortcut, foreign_target)
        shortcut_before = sha256(self.shortcut)
        self._run(
            "uninstall_windows.ps1",
            arguments={
                "ShortcutPath": self.shortcut,
                "ShortcutAllowedRoot": self.allowed,
                "ShellIntegrationScript": self.fake_shell_integration,
                "Confirm": False,
            },
        )
        self.assertTrue(self.shortcut.is_file())
        self.assertEqual(sha256(self.shortcut), shortcut_before)

    def test_uninstall_rejects_owned_shortcut_outside_allowed_root(self) -> None:
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        outside_shortcut = self.outside / "Owned Router.lnk"
        self._make_shortcut(outside_shortcut, self.destination / "ChatGPT.exe")
        before = self._operation_snapshot()
        self._run(
            "uninstall_windows.ps1",
            expected=1,
            arguments={
                "ShortcutPath": outside_shortcut,
                "ShortcutAllowedRoot": self.allowed,
                "ShellIntegrationScript": self.fake_shell_integration,
                "Confirm": False,
            },
        )
        self.assertEqual(self._operation_snapshot(), before)

    def test_cleanup_whatif_is_a_complete_noop(self) -> None:
        self._write_backup("20260101-oldest", "oldest", 30)
        _, newest = self._write_backup("20260103-newest", "newest", 10)
        self._write_backup("20260102-middle", "middle", 20)
        self._write_layout(self.destination, "current", backup_path=newest)
        self.state.mkdir(parents=True)
        before = self._operation_snapshot()
        self._run("cleanup_windows.ps1", arguments={"KeepBackups": 1, "WhatIf": True})
        self.assertEqual(self._operation_snapshot(), before)

    def test_cleanup_retains_newest_and_never_deletes_unauthenticated_content(self) -> None:
        oldest, _ = self._write_backup("20260101-oldest", "oldest", 30)
        middle, _ = self._write_backup("20260102-middle", "middle", 20)
        newest, newest_payload = self._write_backup("20260103-newest", "newest", 10)
        rogue = self.backups / "rogue"
        rogue.mkdir()
        (rogue / "do-not-delete.txt").write_text("untrusted", encoding="utf-8")
        self._write_layout(self.destination, "current", backup_path=oldest / self.destination.name)
        self.state.mkdir(parents=True)
        outside_before = tree_snapshot(self.outside)
        self._run("cleanup_windows.ps1", arguments={"KeepBackups": 1, "Confirm": False})
        self.assertFalse(oldest.exists())
        self.assertFalse(middle.exists())
        self.assertTrue(newest.exists())
        self.assertTrue((rogue / "do-not-delete.txt").is_file())
        manifest = json.loads((self.destination / "codex-mux-build.json").read_text(encoding="utf-8"))
        self.assertEqual(Path(manifest["backupPath"]).resolve(), newest_payload.resolve())
        self.assertEqual(tree_snapshot(self.outside), outside_before)

    def test_cleanup_failed_installations_is_explicit_and_retained_by_count(self) -> None:
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        failed_root = self.state / "failed-installations"
        oldest = failed_root / "20260101-oldest"
        newest = failed_root / "20260102-newest"
        self._write_layout(oldest, "failed-old")
        self._write_layout(newest, "failed-new")
        now = time.time()
        os.utime(oldest, (now - 20, now - 20))
        os.utime(newest, (now - 10, now - 10))
        self._run("cleanup_windows.ps1", arguments={"KeepBackups": 0, "Confirm": False})
        self.assertTrue(oldest.exists())
        self.assertTrue(newest.exists())
        self._run(
            "cleanup_windows.ps1",
            arguments={
                "KeepBackups": 0,
                "IncludeFailedInstallations": True,
                "KeepFailedInstallations": 1,
                "Confirm": False,
            },
        )
        self.assertFalse(oldest.exists())
        self.assertTrue(newest.exists())

    def test_rejects_outside_overlapping_and_reparse_paths_without_mutation(self) -> None:
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        cases = (
            {"Destination": self.allowed},
            {"StateRoot": self.destination / "nested-state"},
            {"BackupRoot": self.outside / "backups"},
        )
        for arguments in cases:
            with self.subTest(arguments=arguments):
                before = self._operation_snapshot()
                self._run("cleanup_windows.ps1", expected=1, arguments=arguments | {"WhatIf": True})
                self.assertEqual(self._operation_snapshot(), before)

        real_backups = self.allowed / "real-backups"
        real_backups.mkdir()
        junction = self.allowed / "junction-backups"
        junction_helper = self.tool / "make-junction.ps1"
        junction_helper.write_text(
            "param([string]$Path,[string]$Target)\n"
            "New-Item -ItemType Junction -Path $Path -Target $Target | Out-Null\n",
            encoding="utf-8",
        )
        junction_result = subprocess.run(
            [
                str(self.shell),
                "-NoProfile",
                "-NonInteractive",
                "-File",
                str(junction_helper),
                "-Path",
                str(junction),
                "-Target",
                str(real_backups),
            ],
            env=self.env,
            cwd=self.work,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=15,
            check=False,
        )
        if junction_result.returncode != 0:
            self.skipTest(f"junction creation unavailable: {junction_result.stdout}")
        before = self._operation_snapshot()
        reparse_target_before = tree_snapshot(real_backups)
        self._run(
            "cleanup_windows.ps1",
            expected=1,
            arguments={"BackupRoot": junction, "WhatIf": True},
        )
        self.assertEqual(self._operation_snapshot(), before)
        self.assertTrue(junction.exists())
        self.assertEqual(tree_snapshot(real_backups), reparse_target_before)

    def test_running_process_from_destination_blocks_uninstall(self) -> None:
        self._write_layout(self.destination, "current")
        self.state.mkdir(parents=True)
        ping = Path(os.environ.get("WINDIR", r"C:\Windows")) / "System32" / "ping.exe"
        if not ping.is_file():
            self.skipTest("Windows ping.exe unavailable for process-path fixture")
        simulated = self.destination / "simulated-router.exe"
        shutil.copy2(ping, simulated)
        process = subprocess.Popen(
            [str(simulated), "127.0.0.1", "-n", "30"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
        )
        try:
            time.sleep(0.5)
            before = self._operation_snapshot()
            result = self._run(
                "uninstall_windows.ps1",
                expected=1,
                arguments={
                    "ShortcutPath": None,
                    "ShellIntegrationScript": self.fake_shell_integration,
                    "Confirm": False,
                },
            )
            self.assertIn("Close Codex Subscription Router", result.stdout)
            self.assertEqual(self._operation_snapshot(), before)
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


if __name__ == "__main__":
    unittest.main()

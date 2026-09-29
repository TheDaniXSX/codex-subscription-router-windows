from __future__ import annotations

import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def write_json(path: Path, value: object) -> str:
    path.parent.mkdir(parents=True, exist_ok=True)
    data = json.dumps(value).encode()
    path.write_bytes(data)
    return digest(data)


@unittest.skipUnless(shutil.which('pwsh'), 'PowerShell 7 required')
class SharedActivationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='csr-activation-')
        self.addCleanup(self.temporary.cleanup)
        # Preparation writes canonical paths. Windows CI can supply an 8.3
        # TEMP alias, so synthetic manifests must use the same path spelling.
        self.root = Path(self.temporary.name).resolve()
        self.operation = self.root / 'prepared'
        self.prod_state = self.root / 'production-data'
        self.dev_state = self.root / 'development-data'
        self.primary = self.root / 'primary'
        self.primary.mkdir()
        self.entries = []
        for index, channel in enumerate(('production', 'development')):
            destination = self.root / channel
            state = (self.prod_state, self.dev_state)[index]
            candidate = self.operation / channel
            state.mkdir()
            (state / 'control-token').write_text('a' * 64 + '\n')
            old_hash = write_json(destination / 'codex-mux-build.json', {'original': channel})
            (destination / 'sentinel.txt').write_text(channel)
            (candidate / 'resources' / 'codex-router').mkdir(parents=True)
            files = {'resources/app.asar': b'asar', 'resources/codex.exe': b'mux',
                     'ChatGPT.exe': b'launcher', 'resources/codex.real.exe': b'official'}
            for name, data in files.items():
                (candidate / name).write_bytes(data)
            binding = {'sharedStateRoot': str(self.prod_state), 'sharedPrimaryHome': str(self.primary),
                       'usageDataRoot': str(self.dev_state), 'sharedProtocol': 1, 'activationPairId': 'fixture-pair-01'}
            manifest = {'schemaVersion': 3, 'destination': str(destination), 'profilePath': str(state / 'Profile'),
                        'installChannel': channel, 'controlPort': 60020 + index, 'patchedAsarSha256': digest(b'asar'),
                        'muxSha256': digest(b'mux'), 'launcherSha256': digest(b'launcher'), 'sourceCodexSha256': digest(b'official'),
                        'primaryCodexHome': str(self.primary), 'primarySqliteHome': str(self.primary), **binding}
            new_hash = write_json(candidate / 'codex-mux-build.json', manifest)
            write_json(candidate / 'resources/codex-router/launcher-config.json',
                       {'schemaVersion': 3, 'stateRoot': str(state), 'controlPort': 60020 + index,
                        'installChannel': channel, 'primaryCodexHome': str(self.primary), 'primarySqliteHome': str(self.primary), **binding})
            write_json(candidate / 'codex-mux-prepared.json', {'schemaVersion': 1, 'destination': str(destination),
                       'stateRoot': str(state), 'activationPairId': binding['activationPairId'], 'controlTokenSha256': digest(b'a' * 64),
                       'controlTokenIsNew': False, 'expectedInstalledManifestSha256': old_hash, 'preparedManifestSha256': new_hash})
            self.entries.append((candidate, destination, state))

    def run_activation(self, *flags, inject_failure=False, failure=None):
        command = [shutil.which('pwsh'), '-NoProfile', '-NonInteractive', '-File',
                   str(ROOT / 'scripts/activate_shared_pair.ps1'), '-ProductionCandidate', str(self.entries[0][0]),
                   '-DevelopmentCandidate', str(self.entries[1][0]), '-AllowedRoot', str(self.root), *flags]
        if inject_failure:
            failure = 'forward'
        if failure:
            harness = self.root / 'inject.ps1'
            harness.write_text(r'''
param([string]$Script,[string]$Production,[string]$Development,[string]$Root,[string]$Failure)
$global:csrActivationSyntheticFailure=$false
$global:csrActivationReverseFailure=$false
$global:csrActivationPostCommitFailure=$false
$global:csrActivationFailureKind=$Failure
$global:csrActivationProduction=$Production
$global:csrActivationDevelopment=$Development
$global:csrActivationDevelopmentBackup=Join-Path (Split-Path -Parent $Production) 'previous-development'
function global:Move-Item {
  param([string]$LiteralPath,[string]$Destination)
  if ($global:csrActivationFailureKind -in @('forward','reverse') -and -not $global:csrActivationSyntheticFailure -and $LiteralPath -eq $global:csrActivationDevelopment) { $global:csrActivationSyntheticFailure=$true; throw 'Synthetic second-publish failure' }
  if ($global:csrActivationFailureKind -eq 'reverse' -and -not $global:csrActivationReverseFailure -and $LiteralPath -eq $global:csrActivationDevelopmentBackup) { $global:csrActivationReverseFailure=$true; throw 'Synthetic reverse-move failure' }
  Microsoft.PowerShell.Management\Move-Item -LiteralPath $LiteralPath -Destination $Destination
  if ($global:csrActivationFailureKind -eq 'crash' -and $LiteralPath -eq $global:csrActivationProduction) { [Environment]::Exit(93) }
}
if ($Failure -eq 'postcommit') {
  function global:Import-Module {
    param([string]$Name,[switch]$Force)
    Microsoft.PowerShell.Core\Import-Module -Name $Name -Force:$Force -Global
    function global:Write-CsrJsonAtomic {
      param($Value,[string]$Path)
      if ([IO.Path]::GetFileName($Path) -eq 'activation-status.json' -and $Value.state -eq 'committed' -and -not $global:csrActivationPostCommitFailure) {
        $global:csrActivationPostCommitFailure=$true; throw 'Synthetic post-commit status failure'
      }
      WindowsLifecycle\Write-CsrJsonAtomic -Value $Value -Path $Path
    }
  }
}
& $Script -ProductionCandidate $Production -DevelopmentCandidate $Development -AllowedRoot $Root
''')
            command = command[:3] + ['-File', str(harness), '-Script', str(ROOT / 'scripts/activate_shared_pair.ps1'),
                                   '-Production', str(self.entries[0][0]), '-Development', str(self.entries[1][0]), '-Root', str(self.root), '-Failure', failure]
        return subprocess.run(command, capture_output=True, text=True, timeout=90)

    def update_bindings(self, *, development_state=None, primary=None):
        if development_state is not None:
            development_state.mkdir(parents=True, exist_ok=True)
            (development_state / 'control-token').write_text('a' * 64 + '\n')
            candidate, destination, _ = self.entries[1]
            self.entries[1] = (candidate, destination, development_state)
            self.dev_state = development_state
        if primary is not None:
            primary.mkdir(parents=True, exist_ok=True)
            self.primary = primary
        for candidate, _, state in self.entries:
            manifest_path = candidate / 'codex-mux-build.json'
            sidecar_path = candidate / 'resources/codex-router/launcher-config.json'
            manifest = json.loads(manifest_path.read_text())
            sidecar = json.loads(sidecar_path.read_text())
            for value in (manifest, sidecar):
                value.update(usageDataRoot=str(self.dev_state), sharedPrimaryHome=str(self.primary),
                             primaryCodexHome=str(self.primary), primarySqliteHome=str(self.primary))
            manifest['profilePath'] = str(state / 'Profile')
            sidecar['stateRoot'] = str(state)
            new_hash = write_json(manifest_path, manifest)
            write_json(sidecar_path, sidecar)
            receipt_path = candidate / 'codex-mux-prepared.json'
            receipt = json.loads(receipt_path.read_text())
            receipt.update(preparedManifestSha256=new_hash, stateRoot=str(state))
            write_json(receipt_path, receipt)

    def test_validate_only_does_not_publish_or_create_journal(self):
        result = self.run_activation('-ValidateOnly')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.prod_state / 'shared-activation.json').exists())
        self.assertFalse((self.operation / 'pre-shared-data').exists())
        for candidate, destination, _ in self.entries:
            self.assertTrue(candidate.exists())
            self.assertTrue((destination / 'sentinel.txt').exists())

    def test_pair_commit_preserves_data_and_old_applications(self):
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(json.loads((self.prod_state / 'shared-activation.json').read_text())['state'], 'committed')
        for index, (candidate, destination, state) in enumerate(self.entries):
            self.assertFalse(candidate.exists())
            self.assertEqual(json.loads((destination / 'codex-mux-build.json').read_text())['schemaVersion'], 3)
            self.assertTrue((self.operation / ('previous-' + ('production', 'development')[index]) / 'sentinel.txt').exists())
            self.assertEqual((state / 'control-token').read_text(), 'a' * 64 + '\n')
        self.assertFalse((self.prod_state / 'usage-ledger.sqlite').exists())

    def test_second_publish_failure_restores_both_and_retains_candidates(self):
        result = self.run_activation(inject_failure=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Synthetic second-publish failure', result.stdout + result.stderr)
        for candidate, destination, _ in self.entries:
            self.assertTrue(candidate.exists())
            self.assertTrue((destination / 'sentinel.txt').exists())
        self.assertEqual(json.loads((self.operation / 'activation-status.json').read_text())['state'], 'rolled-back')

    def test_changed_payload_or_token_rejected_before_moves(self):
        (self.entries[1][0] / 'resources/codex.exe').write_bytes(b'tampered')
        result = self.run_activation()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.prod_state / 'shared-activation.json').exists())
        for candidate, destination, _ in self.entries:
            self.assertTrue(candidate.exists())
            self.assertTrue((destination / 'sentinel.txt').exists())

    def test_data_inside_own_install_rejected_before_moves(self):
        self.update_bindings(development_state=self.entries[1][1] / 'calibration')
        self.assert_overlap_rejected()

    def test_cross_channel_data_overlap_rejected_before_moves(self):
        self.update_bindings(development_state=self.entries[0][1] / 'calibration')
        self.assert_overlap_rejected()

    def test_staging_ancestor_of_data_rejected_before_moves(self):
        self.update_bindings(development_state=self.operation / 'history')
        self.assert_overlap_rejected()

    def test_shared_primary_home_inside_install_rejected_before_moves(self):
        self.update_bindings(primary=self.entries[0][1] / 'native-home')
        self.assert_overlap_rejected()

    def test_candidate_colliding_with_backup_rejected_during_validation(self):
        old, destination, state = self.entries[1]
        new = self.operation / 'previous-production'
        old.rename(new)
        self.entries[1] = (new, destination, state)
        self.assert_overlap_rejected()

    def assert_overlap_rejected(self):
        result = self.run_activation('-ValidateOnly')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('non-overlapping trees', result.stdout + result.stderr)
        self.assertFalse((self.prod_state / 'shared-activation.json').exists())
        for candidate, destination, _ in self.entries:
            self.assertTrue(candidate.exists())
            self.assertTrue((destination / 'sentinel.txt').exists())

    def test_post_commit_status_failure_does_not_roll_back_visible_commit(self):
        result = self.run_activation(failure='postcommit')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Synthetic post-commit status failure', result.stdout + result.stderr)
        self.assertEqual(json.loads((self.prod_state / 'shared-activation.json').read_text())['state'], 'committed')
        status = json.loads((self.operation / 'activation-status.json').read_text())
        self.assertEqual(status['state'], 'committed')
        self.assertIn('Synthetic post-commit status failure', status['postCommitError'])
        for index, (candidate, destination, _) in enumerate(self.entries):
            self.assertFalse(candidate.exists())
            self.assertEqual(json.loads((destination / 'codex-mux-build.json').read_text())['schemaVersion'], 3)
            self.assertTrue((self.operation / ('previous-' + ('production', 'development')[index]) / 'sentinel.txt').exists())

    def test_reverse_failure_attempts_other_restore_and_keeps_launch_barrier_closed(self):
        result = self.run_activation(failure='reverse')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Synthetic second-publish failure', result.stdout + result.stderr)
        self.assertIn('Synthetic reverse-move failure', result.stdout + result.stderr)
        self.assertTrue((self.entries[0][1] / 'sentinel.txt').exists(), 'PROD restoration must still be attempted')
        self.assertFalse(self.entries[1][1].exists())
        self.assertTrue((self.operation / 'previous-development' / 'sentinel.txt').exists())
        self.assertTrue(all(candidate.exists() for candidate, _, _ in self.entries))
        for path in (self.prod_state / 'shared-activation.json', self.operation / 'activation-status.json'):
            status = json.loads(path.read_text())
            self.assertEqual(status['state'], 'recovery-required')
            self.assertIn('Synthetic second-publish failure', status['error'])
            self.assertTrue(any('Synthetic reverse-move failure' in error for error in status['rollbackErrors']))
        self.assertTrue((self.operation / 'activation-plan.json').exists())

    def test_interrupted_publishing_retains_recovery_plan_and_closed_barrier(self):
        result = self.run_activation(failure='crash')
        self.assertEqual(result.returncode, 93)
        self.assertEqual(json.loads((self.prod_state / 'shared-activation.json').read_text())['state'], 'publishing')
        plan = json.loads((self.operation / 'activation-plan.json').read_text())
        self.assertEqual(len(plan['entries']), 2)
        self.assertTrue((self.operation / 'previous-production' / 'sentinel.txt').exists())
        self.assertTrue((self.entries[1][1] / 'sentinel.txt').exists())
        self.assertTrue(self.entries[1][0].exists())
        self.assertEqual(json.loads((self.entries[0][1] / 'codex-mux-build.json').read_text())['schemaVersion'], 3)
        for _, _, state in self.entries:
            self.assertEqual((state / 'control-token').read_text(), 'a' * 64 + '\n')
        # A repeat is deliberately refused instead of guessing how to move
        # partially published trees. The durable plan supports manual recovery.
        repeated = self.run_activation()
        self.assertNotEqual(repeated.returncode, 0)
        self.assertEqual(json.loads((self.prod_state / 'shared-activation.json').read_text())['state'], 'publishing')

    def test_primary_home_outside_managed_root_remains_allowed_and_untouched(self):
        with tempfile.TemporaryDirectory(prefix='csr-native-home-') as outside:
            home = Path(outside).resolve()
            (home / 'history-sentinel').write_text('preserved')
            self.update_bindings(primary=home)
            result = self.run_activation('-ValidateOnly')
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual((home / 'history-sentinel').read_text(), 'preserved')

    def test_cold_snapshot_copies_only_allowlisted_files_and_is_private(self):
        expected = {
            'shared/state.json': (self.prod_state / 'state.json', b'{"accounts":[]}'),
            'shared/routing-mode.json': (self.prod_state / 'routing-mode.json', b'{"mode":"auto"}'),
            'shared/thread-spending.json': (self.prod_state / 'thread-spending.json', b'legacy observation'),
            'shared/thread-spending/' + 'a' * 64 + '.json':
                (self.prod_state / 'thread-spending' / ('a' * 64 + '.json'), b'thread observation'),
            'primary/.codex-global-state.json': (self.primary / '.codex-global-state.json', b'{"projects":[]}'),
            'usage/usage-ledger.sqlite': (self.dev_state / 'usage-ledger.sqlite', b'cold calibration ledger'),
            'usage/usage-ledger.sqlite-journal': (self.dev_state / 'usage-ledger.sqlite-journal', b'recovery journal'),
            'usage/usage-ledger.sqlite-wal': (self.dev_state / 'usage-ledger.sqlite-wal', b'recovery wal'),
            'usage/usage-ledger.sqlite-shm': (self.dev_state / 'usage-ledger.sqlite-shm', b'recovery shm'),
            'usage/usage-relations.json': (self.dev_state / 'usage-relations.json', b'{"relations":[]}'),
        }
        for source, data in expected.values():
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_bytes(data)
        excluded = [self.primary / 'auth.json', self.primary / 'state_5.sqlite',
                    self.prod_state / 'accounts' / 'auth.json', self.dev_state / 'auth.json',
                    self.prod_state / 'thread-spending' / 'unexpected.json']
        for source in excluded:
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_bytes(b'do not copy')
        result = self.run_activation()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        snapshot = self.operation / 'pre-shared-data'
        manifest = json.loads((snapshot / 'snapshot.json').read_text())
        self.assertEqual(manifest['state'], 'complete')
        self.assertEqual(manifest['format'], 'cold-file-set')
        self.assertEqual({item['relative'] for item in manifest['files']}, set(expected))
        for item in manifest['files']:
            source, data = expected[item['relative']]
            self.assertEqual((snapshot / item['relative']).read_bytes(), data)
            self.assertEqual(source.read_bytes(), data)
            self.assertEqual(item['sha256'], digest(data))
        copied = {str(path.relative_to(snapshot)).replace('\\', '/') for path in snapshot.rglob('*') if path.is_file()}
        self.assertEqual(copied, set(expected) | {'snapshot.json'})
        for path in excluded:
            self.assertEqual(path.read_bytes(), b'do not copy')
        plan = json.loads((self.operation / 'activation-plan.json').read_text())
        self.assertEqual(Path(plan['dataSnapshot']), snapshot)
        verify = self.root / 'verify-acl.ps1'
        verify.write_text('param([string]$Module,[string]$Path)\n'
                          '$ErrorActionPreference="Stop"\nImport-Module $Module -Force\n'
                          'Assert-CsrPrivateDirectoryAcl -Path $Path\n')
        checked = subprocess.run([shutil.which('pwsh'), '-NoProfile', '-NonInteractive', '-File', str(verify),
                                  '-Module', str(ROOT / 'scripts/WindowsLifecycle.psm1'), '-Path', str(snapshot)],
                                 capture_output=True, text=True, timeout=30)
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)

    def test_existing_snapshot_is_never_overwritten(self):
        snapshot = self.operation / 'pre-shared-data'
        snapshot.mkdir()
        (snapshot / 'sentinel').write_bytes(b'previous baseline')
        result = self.run_activation()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('snapshot already exists', result.stdout + result.stderr)
        self.assertEqual((snapshot / 'sentinel').read_bytes(), b'previous baseline')
        self.assertFalse((self.prod_state / 'shared-activation.json').exists())
        for candidate, destination, _ in self.entries:
            self.assertTrue(candidate.exists())
            self.assertTrue((destination / 'sentinel.txt').exists())


if __name__ == '__main__':
    unittest.main()

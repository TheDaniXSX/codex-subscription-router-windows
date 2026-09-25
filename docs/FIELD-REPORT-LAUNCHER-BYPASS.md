# Field report: long staging paths and launcher bypass

Observed on a second Windows 11 x64 machine (not the maintainer's) between
2026-09-13 and 2026-09-17. Store package `26.908.4834.0`, router installed from
`7eaf2e9` with the default destination and state root, control port `61289`,
`LongPathsEnabled = 0`. Nothing here was reproduced on a clean VM.

This file records one fix included in the same change and two open issues that
still need a maintainer decision.

## 1. Fixed here: staging copy exceeds `MAX_PATH`

`patch_windows_app.py` created its staging directory as
`codex-subscription-router-staging-XXXXXXXX` next to the destination. With the
default destination under `%LOCALAPPDATA%\Programs`, the 26.908 payload has
relative paths of up to 168 characters, all under
`resources\cua_node\...\classic-level\deps\leveldb\...`.

| Location | Longest resulting path |
| --- | --- |
| Final destination | 231 |
| Staging with the old prefix | 274 |
| Staging with `csr-stg-` | 250 |

Figures are for a five-character Windows user name. With the old prefix,
`shutil.copytree` failed for 111 files with `[WinError 3] The system cannot
find the path specified`, in both `--dry-run` and a real install, so the router
could not be installed at all. The final destination was never the problem.

The prefix is now `csr-stg-`. It still does not start with a dot, which the
existing comment requires for `@electron/asar` unpack matching. A user name
longer than roughly 14 characters would still overflow; a preflight check of the
longest projected staging path, or a documented `LongPathsEnabled` requirement,
would close that gap properly.

## 2. Open: Windows relaunches `ChatGPT.real.exe` without the launcher

Symptom: the profile menu shows `Failed to fetch (127.0.0.1:61289)` and
`0 connected subscriptions`. Chats still work, through the primary account only.

Evidence from the affected session:

- Two top-level `ChatGPT.real.exe` processes, parent `explorer.exe`, started
  about two minutes after sign-in. No `ChatGPT.exe` launcher process existed.
- Both command lines ended with `--restore-last-session --restart`. One carried
  `--user-data-dir=<state root>\Profile`; the other had no user data directory.
- Each spawned its own `resources\codex.exe`, but nothing listened on the
  control port, because the launcher never supplied `CODEX_MUX_CONTROL_PORT`
  and the control token.
- No `Run` key, Startup folder item or scheduled task referenced the router.

This matches Chromium registering the process for application restart, and
Windows reopening registered apps at sign-in using the process's own image
path. That path is `ChatGPT.real.exe`, so the launcher is bypassed after every
reboot or sign-out while the router was open.

Closing the window does not end the session: the app stays alive in the
background, so reopening it through the correct shortcut only focuses the broken
instance. All router processes had to be stopped before starting `ChatGPT.exe`.

Possible directions, not evaluated:

- Have `ChatGPT.real.exe` detect a missing launcher environment at startup and
  re-execute `ChatGPT.exe` instead of continuing.
- Unregister application restart for the patched copy, or register the launcher
  path as the restart command.
- Have the doctor script flag "router processes present, launcher absent,
  control port closed" as a named condition.

### Update 2026-09-24: on the 26.917 build the bypass is fatal

Router built from Store `26.917.6896.0` (bundle `26.917.51856`), installed from
`070ef24`. About two minutes after sign-in, the router and the Store Codex app
started within two seconds of each other, and the router showed a modal error
instead of opening:

```text
Codex Subscription Router failed to start.
The process has no package identity.
```

The trigger was a Windows Update restart. `MoUsoCoreWorker.exe` rebooted the
machine at 03:29 while the router and the Store app were open (System event
1074, "service pack (planned)"), a second servicing reboot followed at 11:00,
the user signed in at 11:03 and both apps reappeared at 11:06. Explorer's
UserAssist history records no launch of either app that day, and `RestartApps`
("restart apps when I sign back in") was `0`. That setting does not cover
update reboots: a process registered with `RegisterApplicationRestart` is
restarted after an update reboot unless it passed `RESTART_NO_REBOOT`, so users
cannot opt out of this relaunch from Settings. Process command lines were not
captured this time, and no log file was written for the failed attempt.
Starting `ChatGPT.exe` afterwards worked normally, and its log shows
`windows_core_runtime_launch_selected ... selectedRuntimeSource=bundled`.

The dialog comes from the bootstrap's catch-all handler
(`${app.getName()} failed to start.`, with the error message as detail). The
bootstrap decides whether it is running as the Store's app-contained core with
a check equivalent to:

```js
process.platform === "win32" && app.isPackaged && process.resourcesPath != null
  && !process.env.CODEX_CLI_PATH?.trim()
  && packageJson.codexWindowsAppContainedCore === "1"
```

The patched `app.asar` still carries `"codexWindowsAppContainedCore": "1"` and
`"codexWindowsPackageIdentity": "OpenAI.Codex"`. Under the launcher the check is
false because the launcher sets `CODEX_CLI_PATH`. Without the launcher it is
true, and the bootstrap then calls the native addon's
`getCurrentPackageFamily()`, which needs a package identity the loose copy does
not have. The message matches `APPMODEL_ERROR_NO_PACKAGE`. Which native call
threw was inferred from the code, not traced.

This is not the remote runtime-framework rollout: the
`electron-windows-core-runtime-frameworks-enabled` and
`electron-windows-primary-runtime-frameworks-enabled` flags in
`~/.codex/.codex-global-state.json` were `false`.

On 26.908 a bypassed start degraded to a working app without subscriptions; on
26.917 it does not start at all, which raises the priority of this issue. One
more direction, also not evaluated: have the patcher clear
`codexWindowsAppContainedCore` in the patched `package.json`, so a bypassed
start falls back to the bundled runtime instead of failing.

## 3. Open: the Start Menu shortcut is retargeted to `ChatGPT.real.exe`

The installer wrote `Codex Subscription Router.lnk` with the launcher as target
and the router AppUserModelID. On 2026-09-14 and again on 2026-09-15, after
being repaired by hand, the shortcut's target had become `ChatGPT.real.exe`.
The modification times fall a few minutes after a router instance started.
It happened three more times: twice on 2026-09-21, and on 2026-09-23 about
twenty minutes after `install_windows.ps1` had rewritten the shortcut with the
correct target.

- Desktop and taskbar-pinned shortcuts with the same target and AppUserModelID
  were not modified.
- The patched `app.asar` contains no `writeShortcutLink`,
  `setLoginItemSettings` or `openAtLogin` call, and its only `Start Menu`
  references are read-only application detection. The rewrite most likely comes
  from native Chromium shortcut maintenance keyed on the AppUserModelID. This
  was not confirmed.

Launching from the rewritten shortcut produces the same symptom as issue 2,
which on the 26.917 build means the startup failure described in its update.

## 4. Open: ACL hardening fails under Windows PowerShell 5.1 on long paths

Seen on 2026-09-23 while updating the same machine from the 26.908 build to
26.917 with `install_windows.ps1 -Force`. The build, patch and verification
steps succeeded; the step `Hardening private router payload and state ACLs`
then failed twice with `Could not find a part of the path`, and the automatic
rollback restored the previous build each time.

`Set-CsrPrivateDirectoryAcl` and `Assert-CsrPrivateDirectoryAcl` enumerate the
whole state root and backup root with `Get-ChildItem -Recurse`. Windows
PowerShell 5.1 cannot open paths longer than 260 characters even with
`LongPathsEnabled`, and two trees under those roots exceed it:

| Tree | Longest path | Count over 260 |
| --- | --- | --- |
| `Data\accounts\<id>\codex-home\.tmp\plugins` (Codex bundled-plugin cache, one per account) | 289 | 101 per account |
| `.codex-subscription-router-backups\<stamp>\Codex Subscription Router\resources\cua_node\...` | about 290 | 111 |

The first tree is regenerated by Codex; the second is created by the installer
itself when it moves the previous build aside, so on this machine every update
fails while a backup is retained. A failed attempt also leaves the new build
under `Data\failed-installations\<stamp>`, which contains the same long paths
and makes the next attempt fail the same way until it is deleted with
long-path-aware tooling.

Running the same command under PowerShell 7.6.6 (portable, unsigned MSI not
needed) completed the update and all 53 verifier checks. PowerShell 7 supports
long paths in `Get-ChildItem`, `Get-Acl` and
`[IO.FileSystemAclExtensions]::SetAccessControl`. Options: require PowerShell 7
in the installer's prerequisite check instead of allowing 5.1, or enumerate
with `\\?\`-prefixed paths and skip `.tmp` caches and the backup root during
hardening.

## Workaround used on the affected machine

A local script, not part of this repository, stops every process whose image
path is under the router destination and then starts `ChatGPT.exe`. It asks for
confirmation first, since it interrupts running work.

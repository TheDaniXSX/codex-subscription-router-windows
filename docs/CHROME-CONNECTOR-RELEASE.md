# Chrome connector release gate

The Windows port now has an independent, opt-in Native Messaging
implementation. Its identity is:

- extension: `chrome-extension/` source, with the production ID assigned only
  by Chrome Web Store;
- native host: `io.github.thedanixsx.codex_subscription_router`;
- registry: per-user `HKCU\Software\Google\Chrome\NativeMessagingHosts\` plus
  the router-owned host name;
- IPC: length-prefixed JSON protocol `codex-router-native-v1` over stdio;
- router authentication: the native host reads the ACL-protected control token
  and sends it only in the loopback request header. The extension never sees
  that token.

At each Chrome launch, the host reads the installed schema-2
`launcher-config.json` and `codex-mux-build.json`, requires their dynamically
allocated `controlPort` values to match in `49152..65535`, and derives the
loopback URL. There is no fixed-port release fallback. Development builds use
the Windows GUI subsystem so a Native Messaging activation does not flash a
console window.

Captured page contexts are private current-user files. The host keeps at most
20 context records so page data cannot grow without bound.

The feature remains disabled by default. Building the desktop application does
not register a host and does not install or open Chrome. The connector install
is a separate, explicit step because `allowed_origins` must name the exact
published extension ID.

## Development qualification without touching Chrome

Repository safety can be checked with:

```powershell
pwsh -NoProfile -File scripts/verify_chrome_connector.ps1 -RepositoryOnly
go test ./internal/chromenative ./cmd/chrome-native-host
```

The install/uninstall scripts expose a hidden filesystem registry fixture for
automated tests. Production usage omits that parameter and uses HKCU.

## Opt-in local test

1. Load `chrome-extension/` as an unpacked extension in an isolated Chrome test
   profile.
2. Copy the 32-character extension ID shown by Chrome.
3. Stop Chrome and run:

```powershell
pwsh -NoProfile -File scripts/install_chrome_native_host.ps1 -ExtensionId <id>
```

The installer rejects a differently owned registry value. Uninstall compares
the exact registry path and SHA-256 values from `ownership-receipt.json`; it
preserves missing or modified artifacts and always preserves router state.

```powershell
pwsh -NoProfile -File scripts/uninstall_chrome_native_host.ps1
```

## Public-release gate

Do not advertise Chrome parity until all of the following exist:

1. a published Chrome Web Store listing and final extension ID;
2. a trusted Authenticode signature on `codex-router-chrome-host.exe`;
3. a clean-VM qualification report covering install, authenticated hello and
   health, user-initiated HTTP capture, non-HTTP rejection, restart, upgrade,
   exact compare-and-delete, and official-installation invariance;
4. desktop consumption of the staged browser context, qualified end to end.

The mechanical part of the gate is enforced by
`scripts/verify_chrome_connector.ps1 -ReleaseGate`. Publication credentials,
signing material, and Chrome Web Store ownership must not be committed.

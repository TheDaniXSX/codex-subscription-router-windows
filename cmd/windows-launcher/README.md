# Windows launcher

The Windows router package places the Go launcher at the application root as
`ChatGPT.exe`. The unmodified Electron executable remains beside it as
`ChatGPT.real.exe`; the multiplexer and original CLI live at
`resources\codex.exe` and `resources\codex.real.exe` respectively.

Build a GUI-subsystem executable without requiring MSVC:

```powershell
go build -trimpath -ldflags "-s -w -H=windowsgui" -o ChatGPT.exe ./cmd/windows-launcher
```

The required UTF-8 sidecar at
`resources\codex-router\launcher-config.json` is strict, versioned, and contains
no credentials:

```json
{"schemaVersion":2,"stateRoot":"D:\\absolute\\router-state","controlPort":61234}
```

`controlPort` is generated and persisted by the installer and must be in the
dynamic/private range `49152..65535`. The launcher exports it as
`CODEX_MUX_CONTROL_PORT`, replacing any inherited value so Electron and the mux
share one endpoint. Schema 1 is parsed only for read-only migration diagnostics;
it reports the historical port and an upgrade-required warning. New builds must
always emit schema 2 and never choose the old fixed port.

The sidecar state root and control port are one canonical configuration unit.
Inherited `CODEX_ROUTER_DATA_DIR`, `CODEX_MUX_HOME`, and
`CODEX_MUX_STATE_ROOT` values cannot redirect a release launch; the launcher
replaces them with the persisted state root before Electron starts.

The launcher strips inherited `CODEX_MUX_CONTROL_TOKEN` and
`CODEX_MUX_UI_TESTS` values. Release tokens come only from the persisted build
contract, and deterministic UI test routes cannot be enabled by the parent
process environment.

The launcher rejects relative roots, malformed/unknown sidecar fields, and
incoming `--user-data-dir` overrides. It injects the isolated profile switch,
sets the Codex CLI interception gate, preserves every other argument (including
`codex://` deep links), forces `CODEX_SPARKLE_ENABLED=false` as a second updater
defense, waits for the real app, and returns its exit code. Before Electron
starts, the launcher places itself in an unnamed Windows Job Object with
`KILL_ON_JOB_CLOSE`; the complete Electron descendant tree is therefore cleaned
up if the launcher is terminated or the real desktop exits unexpectedly. It
deliberately preserves `CODEX_HOME` and `CODEX_SQLITE_HOME`, keeping the
currently signed-in account primary.

The optional unpackaged `codex-router://` registration forwards only
`codex-router://open?path=<encoded absolute local existing path>`. The launcher
validates that private URI, rejects extra arguments and injection shapes, and
converts it to one literal path argument. It does not register or reinterpret
the official `codex://` scheme. See
[`docs/WINDOWS-SHELL-INTEGRATION.md`](../../docs/WINDOWS-SHELL-INTEGRATION.md).

Appshots remains experimental and is disabled by default. A user may opt in for
a validation session by setting `CODEX_ROUTER_ENABLE_APPSHOTS=1` before launch.
Conflicting or alternative truthy values fail closed. There is intentionally no
force-stop command: normal shutdown belongs to Electron, while the Job Object
handles orphan prevention without PID matching or a cross-process control API.

`ChatGPT.exe --router-self-test` performs a read-only, no-spawn validation and
prints the selected source/root plus all resolved executable paths.
`ChatGPT.exe --router-diagnostics` adds the supervision mode and current
Appshots gate state; it also never starts or stops the real application.

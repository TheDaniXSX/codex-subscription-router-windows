# Windows security model

This document defines the security boundary and release requirements for the
Windows port. It is intentionally stricter than a description of the current
implementation: **MUST** and **MUST NOT** statements are release gates, while
**SHOULD** statements require either implementation or a documented exception.

The router is an unofficial, locally patched copy of the official Codex
desktop application. It does not turn multiple subscriptions into one security
principal. Every account remains an independent OpenAI identity, and every
operator is responsible for complying with the terms that apply to those
accounts.

## Assets and security objectives

The assets that need protection are:

- OAuth access and refresh material for every Codex subscription;
- MCP OAuth credentials and inline MCP environment secrets;
- private conversation databases, project paths, prompts, and tool output;
- the random control API bearer token;
- account metadata and the persistent thread-to-account ownership map;
- patch inputs, generated binaries, signing keys, and update metadata; and
- recoverable backups of generated applications and router state.

The design aims to preserve confidentiality between different Windows users,
prevent remote access to the control API, reject unreviewed upstream binaries,
and make installation and rollback transactional. It does **not** claim to
protect secrets from malware, debuggers, administrators, or another process
already running as the same Windows user. Such a process can generally inspect
the user's files and process memory regardless of the router.

## Threat model

The supported threat model includes:

- a website attempting cross-origin or DNS-rebinding requests to the loopback
  control service;
- another non-elevated Windows user attempting to read router data;
- another local process guessing the control port without possessing its
  bearer token;
- a partial, malicious, or unexpectedly changed official Codex update;
- tampering with the patcher, mux, installed files, or an update artifact;
- accidental disclosure through logs, command lines, temporary files, crash
  dumps, backups, or cloud-synchronized folders;
- path substitution through junctions, symbolic links, or other reparse
  points during install, update, or state creation;
- a compromised MCP server or a secret embedded in synchronized MCP
  configuration; and
- interrupted installs and incompatible updates that leave a non-starting
  patched application.

The following are outside the isolation guarantee but still require sensible
hardening: compromise of the current Windows account, an elevated
administrator, kernel malware, a compromised official OpenAI binary, a
compromised signing key, and attacks on OpenAI's service. Account isolation is
primarily routing and storage separation, not a sandbox against hostile code.

## Windows hardening baseline

The Windows port replaces inherited POSIX assumptions with controls that are
verified as release gates:

1. Sensitive state, generated application resources, backups, and preserved
   failed installations use protected Windows DACLs. Allow ACEs are limited to
   the current user SID and LocalSystem and are verified by SID, not localized
   account names.
2. Each installation receives a CSPRNG-selected port in the Windows dynamic
   range `49152..65535`. The same value is stored in the private schema-v2
   launcher sidecar and build manifest and is injected into the local ASAR.
   The launcher exports it as `CODEX_MUX_CONTROL_PORT`; the mux rejects a
   missing, fixed legacy, malformed, or out-of-range value. It reserves
   `127.0.0.1:<controlPort>` before opening account state or starting children,
   and a collision is fatal. There is no release fallback to port `48123`.
3. Router-owned process trees are supervised with Windows Job Objects and are
   selected by handles and verified paths, not broad image-name termination.
4. A per-user instance lock serializes mux state, and lifecycle operations use
   their own protected lock and validated manifests.
5. Private files are written through uniquely named temporary files in the
   protected destination, flushed, and replaced atomically. Reparse points and
   paths escaping the expected roots are rejected.
6. Managed configuration is rendered deterministically and unchanged content
   is not rewritten.

The implementation is not accepted merely because these controls exist in
source. The CI, installed-build verifier, and release checklist must all pass
on the exact release commit.

## Trust boundaries

```text
Microsoft / OpenAI distribution
        |  Authenticode, publisher, version and SHA-256 verification
        v
Patch and build workspace  ----->  independently signed router installation
                                             |
                                one desktop app-server stream
                                             v
                                      codex-mux.exe
                                  /          |          \
                         Primary child   Account B   Account C
                         primary home    isolated    isolated
                                  \          |          /
                                   OpenAI and configured MCPs

Injected renderer --authenticated loopback--> local control API
```

The boundaries are:

1. **Official build input.** The installed official Codex package is trusted
   only after its Authenticode signature, expected publisher, version,
   architecture, and allowlisted file hashes have been verified. The official
   installation is read-only input and MUST never be edited in place.
2. **Patcher and installer.** These have deliberate access to application
   binaries, the build workspace, and the developer signing identity. They
   MUST NOT read account credentials and SHOULD run without elevation in a
   per-user installation.
3. **Patched desktop and mux.** These are trusted local code. The mux may start
   official Codex children and route private RPC traffic, so compromise here
   compromises all connected subscriptions.
4. **Account homes.** A child receives one `CODEX_HOME` and one
   `CODEX_SQLITE_HOME`. The router prevents accidental cross-account routing;
   it does not sandbox one child from other files readable by the same user.
5. **Injected renderer and control API.** Renderer content may request account
   operations only after authenticating to a loopback-only service. Web
   content, remote hosts, other Windows users, and unauthenticated local
   processes are outside this boundary.
6. **MCP servers and Apps.** These are separate programs or remote services
   with the permissions granted in Codex. Sharing their definitions does not
   make them trusted, nor does it make their credentials account-specific.
7. **Build and runtime state.** Source code, generated application files, user
   state, and backups are separate roots. No official binary, extracted ASAR,
   token, or user database may be committed or published as a project release.

## Filesystem layout and Windows ACLs

The target per-user layout is:

| Data | Default location |
| --- | --- |
| Generated application | `%LOCALAPPDATA%\Programs\Codex Subscription Router` |
| Router state | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\state` |
| Secondary homes | `...\state\accounts\<random-id>\codex-home` |
| Independent Chromium profile | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\Profile` |
| Recoverable application backups | `%LOCALAPPDATA%\Programs\Codex Subscription Router Data\backups` |
| Primary Codex home | `%USERPROFILE%\.codex` (not owned by this project) |

Runtime code MUST resolve these to absolute canonical paths before a write. It
MUST reject a state, staging, destination, or backup path that is a reparse
point or escapes its expected root. Installation MUST NOT write into
`C:\Program Files\WindowsApps`, modify the registered official package, or rely
on ownership changes to protected package files.

POSIX modes passed to Go's `os.Chmod` do not enforce owner-only access on
Windows. Every directory and file containing private state MUST have a
protected DACL built from SIDs, not localized account names:

- the current interactive user's SID: full control;
- `NT AUTHORITY\SYSTEM`: full control, to allow Windows recovery and backup;
- no inherited `Users`, `Authenticated Users`, or `Everyone` access; and
- no added administrator ACE merely for convenience. Administrators retain
  their normal ability to take ownership, which is an explicit threat-model
  limitation.

The installer MUST create and verify the protected parent before writing any
secret. Child objects MUST inherit only that protected DACL or receive an
equivalent explicit DACL. Temporary files MUST be created in the protected
destination directory, opened without sharing that permits replacement, and
committed atomically. ACL validation MUST fail closed; a warning followed by
startup is insufficient.

Application binaries can be readable and executable by the current user, but
only the current user and the installer/update path may modify them. Signing is
not a substitute for a non-writable install ACL.

## Credential and token storage

The Primary account continues to use the credential store selected by the
official Codex configuration. The router MUST NOT copy, transform, delete, or
back up the Primary account's `auth.json` as part of a normal install or update.
Profile aggregation and usage-reset calls that require direct bearer access
MUST return a stable `credentials_unavailable` result when `auth.json` is not
available. They MUST NOT scrape Windows Credential Manager, fall back to a
different account, or return an aggregate that silently omits the affected
subscription. Public account fields may instead be obtained through that
account's official App Server interface when the protocol provides them.

Each secondary account uses a distinct home and forces file-backed Codex and
MCP OAuth credential stores so the corresponding official child can consume
them. Those files MUST be protected by the owner-only DACL described above.
Because the official child must read them, applying DPAPI only in the router
would not provide transparent at-rest encryption. File-backed credentials are
therefore a documented residual risk on a compromised user session or offline
disk. Windows device encryption or BitLocker is recommended for protection at
rest.

The mux state file may contain account IDs, labels, paths, and sticky thread
ownership. It MUST NOT contain OAuth tokens, device codes, passwords, full
profile responses, or MCP secrets. Authentication responses MUST be returned
only to the requesting local UI and MUST not be logged.

The control API token MUST:

- contain 32 bytes from the operating-system CSPRNG, encoded as 64 hexadecimal
  characters;
- never appear in source control, a command line, URL, public release artifact,
  diagnostic bundle, workflow output, or general-purpose log;
- be compared in constant time and rotated after suspected disclosure; and
- be created and replaced through a protected atomic write while the router is
  stopped.

The current source-only Windows release injects the token into the two local
renderer bundles while building from the user's official installation. That
locally generated ASAR is therefore secret-bearing state even though the
upstream ASAR is not. The installer MUST apply and verify the same protected
user-and-LocalSystem DACL on the generated application root, all application
backups, and preserved failed installations before the ASAR exists there. A
build or verifier must compare only a digest or a redacted marker; it must
never print the token. Public releases MUST NOT contain the generated ASAR or
any composite application tree.

Rotating the token requires a stopped transactional rebuild so the state file
and both injected renderer copies change together. A future main/preload IPC
bootstrap should remove this duplication; until then the install-root DACL is
part of the authentication boundary, not merely an integrity control.

A process environment override is acceptable only for automated tests. Release
launchers MUST clear test token variables before starting Codex children and
MUST NOT propagate the control token to child environments.

## Loopback control API

The control listener MUST bind explicitly to `127.0.0.1`, never to `localhost`,
`0.0.0.0`, an external interface, or IPv6 unless `[::1]` is separately and
deliberately supported. Release builds MUST use the identical CSPRNG-selected
high port from the protected launcher sidecar, build manifest, and injected
renderer. Startup MUST acquire the endpoint before loading account credentials
or starting any Codex child, then verify the resulting socket address. Failure
or a collision aborts startup; it MUST NOT cause a fallback to `48123`, a
different port, or a broader address. A future main/preload handle bootstrap or
per-user DACL-protected named pipe can provide stronger endpoint ownership than
an unpredictable loopback port.

Only `/v1/health` may be unauthenticated, and it may return only a fixed status
and non-sensitive version information. Every other endpoint, including the SSE
event stream and test endpoints, MUST require `X-Codex-Mux-Token`. Tokens MUST
NOT be accepted in query parameters because URLs can leak through history,
referrers, telemetry, proxy logs, and screenshots. The event stream should use
`fetch()` with a request header instead of native `EventSource` if necessary.

Browser-facing requests with an `Origin` header MUST match the exact packaged
desktop origin expected by the patched application. An unexpected origin is a
403 response, not merely a response without CORS headers. Requests without an
origin remain available to authenticated native diagnostics. Preflight
responses expose only required methods and headers. The service MUST also set
`Cache-Control: no-store`, `Referrer-Policy: no-referrer`, and
`X-Content-Type-Options: nosniff`.

Additional release requirements are:

- constant-time bearer-token comparison;
- strict per-route method checks, unknown-field rejection, exactly one JSON
  value per body, and explicit rejection above the body-size limit;
- bounded headers, request bodies, responses, and merged history results;
- read-header, request, upstream, idle, and graceful-shutdown timeouts;
- idempotency identifiers for operations such as consuming reset credits;
- no reflection of secrets or raw upstream response bodies in errors; and
- no following redirects for outbound profile or reset requests carrying an
  OAuth bearer; and
- UI-test routes compiled out or gated by an explicit test-only launch flag,
  still authenticated, and absent from release launchers.

The bearer token primarily prevents drive-by browser requests and accidental
access. It cannot isolate the API from malicious code running as the same user
and able to inspect the desktop process or protected generated ASAR.

## Account isolation and child processes

Each enabled account MUST have a unique canonical home. The mux MUST reject
duplicate, nested, missing, network, removable, or reparse-point account homes.
Account identifiers come from a CSPRNG and are data, never path fragments
accepted from API callers.

Children MUST receive only the environment required for the official App
Server. The launcher MUST remove router control tokens, test flags, signing
variables, installer paths, and credentials belonging to other accounts.
Inherited handles should be disabled except for the intended standard streams.
Windows Job Objects SHOULD terminate descendant processes when the mux exits,
without breaking supported Codex helpers.

Disabling an account excludes it from new routing but does not delete its
credentials or history. Logging out affects only that account. Deleting an
account, if later implemented, must be a separate explicit operation with a
preview of the exact path and a recoverable retention policy.

## Synchronized MCP configuration and secrets

The router synchronizes desktop-managed configuration from Primary so plugin,
skill, marketplace, and MCP definitions remain consistent. Project trust and
credential-store selection remain local to each account.

This sync has an important consequence: inline secrets in MCP definitions,
including `env` values, command arguments, headers, or URLs, are copied into
every isolated account's `config.toml`. Those account homes are therefore
**not independent secret boundaries** for shared MCP configuration. The copy
must receive the protected file DACL and must never appear in logs or diffs.

MCP OAuth credentials are different: they are created and stored inside the
selected account home and MUST NOT be copied from Primary. Synchronization
MUST use a parsed allowlist of managed configuration sections where practical;
it MUST preserve each account's project trust and credential settings. A
temporary file and atomic replace prevent truncated configuration.

Operators SHOULD prefer MCP servers that read secrets from Windows Credential
Manager or another external secret provider instead of inline TOML values. A
future secret-reference feature must copy references, not resolved plaintext.
Changing or removing an inline secret in Primary must trigger a resync so stale
copies do not persist indefinitely.

MCP configuration is executable configuration. A synchronized local command
can run with the user's permissions from every account. The UI must make the
source and command visible before enabling it; synchronization is not a trust
decision.

## Patching, signing, and build provenance

Before extraction, the patcher MUST verify the source package and relevant
executables with Windows Authenticode, require the expected OpenAI publisher,
record the version and architecture, and compare allowlisted SHA-256 hashes.
It MUST then verify exact ASAR and binary patch anchors and expected occurrence
counts. `--allow-untested-source` is diagnostic only and MUST NOT produce a
releasable build.

Builds occur in a newly created, owner-controlled staging directory outside
the official package. The patcher MUST never follow links from the source or
staging tree and MUST remove staging data after success or failure. Generated
logs record hashes and versions but no user state or tokens.

The mux, launcher, updater/installer, native helpers, and final package MUST be
signed with the project's Windows code-signing identity for distribution.
The final verification checks the full chain, expected subject/thumbprint,
timestamp, package identity, architecture, and hashes of patched components.
Unsigned developer builds must be conspicuously marked and cannot satisfy the
release smoke test. A valid signature proves publisher and integrity; it does
not prove that the software is safe.

Signing keys MUST live outside the repository, must not be exported into build
artifacts, and SHOULD be hardware-backed or held by a protected CI signing
service. CI secrets must not be available to pull-request jobs from forks.
Release manifests and checksums are signed and generated from a clean, pinned
commit with locked dependencies.

Every pull request is subject to dependency review and full-history secret
scanning. CodeQL runs the extended security-and-quality suites for Go,
JavaScript, and Python. Windows CI additionally runs `govulncheck`, npm audit,
the project license allowlist, repository-content verification, and
unit/integration tests. Third-party Actions are pinned to immutable commit SHAs,
and Dependabot tracks their updates. Scanners run without signing credentials and with the
minimum read permissions; CodeQL alone receives permission to upload security
events. A finding is triaged privately under [`SECURITY.md`](../SECURITY.md),
not copied into a public issue when it could expose an exploit or secret.

The router uses its own application/package identity, Chromium profile, URL
scheme, protocol registration, and native-helper identity. It MUST NOT reuse
the official app's identity or attempt to inherit its permissions. No
installer or test may disable Defender, SmartScreen, UAC, or Windows security
features, or add an antivirus exclusion.

## Updates

The patched copy's upstream self-updater MUST be disabled. The official Codex
app may update normally, but a new official version is only new build input.
It is never copied over a working router installation without compatibility
review.

An update follows this transaction:

1. Verify source provenance, compatibility metadata, hashes, patch anchors,
   toolchain versions, and free disk space.
2. Build and sign in a fresh protected staging directory.
3. Run static checks, unit/integration tests, signature verification, and a
   non-destructive launch probe against an isolated test profile.
4. Stop only router-owned processes. Do not terminate the active official app
   or unrelated `codex.exe` processes by image name alone.
5. Move the existing generated application to a timestamped backup, then move
   the staged application into place using same-volume atomic renames.
6. Launch and run the post-install smoke test. On failure, stop the failed new
   build and atomically restore the previous known-good application.
7. Leave account homes, the primary home, and the independent profile in
   place. Schema migration must be versioned, forward-tested, and backed up.

The updater MUST fail closed on an unknown official version, publisher,
signature, hash, architecture, anchor count, signing identity, or state schema.
It MUST not silently download or execute arbitrary latest-build URLs. Update
metadata must be HTTPS-delivered and signature-verified, with protection
against downgrade to a known vulnerable router release.

## Backups and recovery

Automatic rebuild backups contain only the previously generated application
and a small manifest of versions, hashes, and timestamps. Credentials and
conversation databases live outside the application and MUST NOT be duplicated
for every update. Backup directories receive the same protected DACL as state,
are never placed in OneDrive or another synchronized directory by default, and
have a documented retention limit.

Before a state-schema migration, the router creates an atomic protected copy of
`state.json` and related non-credential metadata. A full export of secondary
account homes is a separate, explicit user action because it contains tokens
and conversations. Such an export must warn about its contents and use strong
encryption; a plain ZIP is not acceptable.

Recovery priorities are:

1. Stop router-owned desktop, mux, and child processes.
2. Preserve the failed application, logs, and state metadata without exposing
   secrets.
3. Verify and restore the most recent known-good generated application.
4. Validate state schema and ACLs before launch; restore the metadata snapshot
   if migration failed.
5. Start against the existing account homes and verify account identity before
   sending a prompt or consuming a reset.

Uninstalling the router removes generated application files and registrations,
but preserves router state and secondary account homes unless the user chooses
a separately confirmed data-removal operation. It never removes the official
Codex package or `%USERPROFILE%\.codex`.

After suspected credential compromise, recovery is not just a file rollback:
log out or revoke every affected subscription and MCP authorization, rotate
inline MCP secrets, rotate the control token, inspect persisted configuration,
and rebuild from a verified clean commit. A compromised signing key requires
revocation and a new release identity.

## Logs, diagnostics, and crash data

Release logging uses structured events and redaction. Logs MUST NOT include
OAuth tokens, control tokens, device codes, cookies, authorization headers,
inline MCP values, full emails, prompt contents, tool output, or raw JSON-RPC
payloads. Account IDs should be truncated or replaced with per-run opaque
identifiers. File paths are private data and should be minimized.

Diagnostic bundles are opt-in, show the exact files to be collected, and redact
before archival. Windows Error Reporting or locally configured crash dumps may
capture process memory containing credentials; dumps inherit the protected DACL
and are never uploaded automatically by this project.

## Release security verification

A Windows build is not releasable until automated tests demonstrate:

- source and final Authenticode verification fail on publisher, hash, and
  tamper mismatches;
- unknown app versions and incorrect patch-anchor counts fail before any
  destination change;
- a second local Windows user cannot enumerate or read state, credentials,
  backups, or the Chromium profile;
- ACL tests compare SIDs and protection flags, not localized display strings;
- reparse points and paths escaping the allowed roots are rejected;
- only `127.0.0.1` is listening and remote-interface connection attempts fail;
- two fresh builds select high, non-legacy ports and the sidecar, manifest,
  launcher environment, ASAR literals, verifier, doctor, and optional Chrome
  host all agree on the exact value;
- a missing, malformed, out-of-range, or inconsistent port fails closed, and
  no release path silently uses `48123`;
- private endpoints reject missing, malformed, and incorrect tokens, reject
  unexpected origins, and never accept a token in the query string;
- no long-lived control token is embedded in application resources readable by
  another local user;
- request, header, response, and timeout limits are enforced;
- tokens and MCP secrets are absent from process command lines, child
  environments, logs, crash-test output, public artifacts, and unprotected
  paths; local application backups that contain an injected ASAR remain under
  the private application DACL;
- Job Object tests terminate every router-owned descendant on normal exit,
  crash, and update while unrelated official Codex processes survive;
- two concurrent router/update processes are serialized, abandoned-lock
  recovery validates state, and unique temporary files cannot collide;
- an unchanged managed configuration sync performs no write and preserves the
  destination content, timestamp, and active Codex watcher state;
- Primary and secondary authentication, logout, MCP OAuth, history, and thread
  ownership remain account-scoped;
- interrupted install and incompatible update simulations restore the previous
  application while retaining state; and
- uninstall and rollback never mutate the official installation or Primary
  Codex home.

Repository release setup must additionally confirm GitHub private
vulnerability reporting is enabled and the advisory link in `SECURITY.md` is
usable by a non-maintainer. Manual smoke testing must confirm Windows Security
reports the expected signer, protocol links open only the router identity,
firewall state is unchanged, and no elevation or security exclusion was
requested.

## Residual risks

- The port depends on undocumented desktop implementation details. A verified
  official update can still change behavior in a way the current anchors do
  not detect.
- All enabled subscriptions ultimately trust the patched renderer, mux, and
  local user session. Compromise of any of those can expose every account.
- File-backed secondary credentials are readable to the current user and to an
  administrator who deliberately takes ownership.
- Inline MCP secrets are intentionally replicated with shared configuration.
- A protected random high loopback port makes cross-user pre-binding unlikely,
  but it is not a cryptographic endpoint-ownership primitive. An attacker that
  can reserve the exact private port before startup may still cause denial of
  service. A trusted inherited socket or DACL-protected named pipe remains a
  defense-in-depth improvement.
- Code signing and loopback authentication reduce attack surface but do not
  sandbox the router or MCP servers.
- The project cannot guarantee that combining subscriptions is permitted for a
  particular user or organization; that policy decision remains external.

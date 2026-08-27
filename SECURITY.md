# Security policy

## Supported versions

Security fixes are made only for the most recent Windows release and the
current default branch. The original upstream macOS release is not maintained
by this fork.

| Version | Supported |
| --- | --- |
| `0.2.x` | Yes |
| `0.1.x` and earlier | No |

Until `0.2.0` is published, treat the default branch as pre-release software.
Compatibility with official Codex packages is narrower than project-version
support and is recorded in [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md).
Unknown official package versions fail closed.

## Report a vulnerability privately

Do not open an issue, discussion, pull request, or public proof of concept for
a suspected vulnerability. Submit a
[private GitHub security advisory](https://github.com/TheDaniXSX/codex-subscription-router-windows/security/advisories/new)
instead. Private vulnerability reporting MUST be enabled before a public
release. If GitHub does not offer the form, do not disclose technical details;
open a generic issue asking the owner to restore the private channel, or wait
until the form is available.

Include, where applicable:

- the project version, exact commit, and installation type;
- the official Codex package, desktop, and CLI versions used as input;
- Windows version and whether the reporter used an administrator account;
- minimal reproduction steps, security impact, and affected trust boundary;
- whether the official application was also affected; and
- redacted logs or hashes needed to reproduce the issue.

Never submit live OAuth tokens, device codes, cookies, authorization headers,
MCP secrets, account identifiers, signing keys, private prompts, conversation
content, database files, or crash dumps. Revoke exposed credentials before
preparing a redacted report.

The maintainer aims to acknowledge a report within three business days and
provide an initial triage within seven business days. Remediation and
disclosure timing depend on severity and whether an upstream vendor must act.
These are response targets, not service-level guarantees. Please allow a
reasonable coordinated-disclosure period before publishing details.

## In scope

- authentication or authorization bypass in the loopback control service;
- cross-origin, DNS-rebinding, request-smuggling, or unsafe JSON parsing paths;
- account-crossing routing, credential disclosure, or unsafe reset redemption;
- arbitrary code execution or path traversal in the patcher, installer,
  launcher, lifecycle commands, packaging, or optional Windows integrations;
- signing, source-verification, rollback, update, or release-provenance bypass;
- ACL, reparse-point, temporary-file, mutex, or process-lifecycle flaws that
  break the documented Windows boundary; and
- committed secrets or distribution of prohibited official/composite binaries.

Reports about OpenAI's unmodified applications or services, GitHub, Windows,
or a third-party MCP server belong with the corresponding vendor. A process
already executing as the same Windows user, an administrator, or kernel code
is outside the isolation guarantee, although a practical privilege escalation
or secret disclosure caused by this project remains in scope.

## Research expectations

Use accounts, machines, and data you own or are authorized to test. Do not
access another person's subscriptions, consume real reset credits without
permission, degrade OpenAI or GitHub services, persist after demonstrating the
minimum impact, or retain private data. Non-destructive tests against a local
synthetic fixture are strongly preferred.

Good-faith research that follows this policy will not be intentionally pursued
by this project merely for bypassing a project control while demonstrating a
vulnerability. This statement cannot authorize testing against systems owned
by OpenAI, Microsoft, GitHub, or another party.

## Security design and release evidence

The complete threat model, trust boundaries, residual risks, and release gates
are in [`docs/WINDOWS-SECURITY.md`](docs/WINDOWS-SECURITY.md). Security checks
in Git include CodeQL, full-history secret scanning, dependency review,
`govulncheck`, npm audit, license policy, source-artifact verification, and
Windows-specific tests. Passing automation is necessary but does not replace
code review, Authenticode verification, or the manual release checklist.

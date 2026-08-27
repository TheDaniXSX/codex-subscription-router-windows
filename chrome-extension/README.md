# Router-owned Chrome connector

This directory is an independent Manifest V3 extension. It does not contain,
copy, update, register, or remove the official OpenAI extension or its native
host `com.openai.codexextension`.

The source intentionally has no committed Chrome extension ID. Chrome Web
Store assigns the production ID when the extension is published. Registration
of the native host is therefore opt-in and the installer requires that exact
32-character ID. For local qualification, load this directory unpacked, copy
the ID shown by `chrome://extensions`, and use it only in a test installation.

The extension uses `activeTab`: it captures an HTTP(S) page only after the user
clicks **Share this page**. It does not request global host permissions. Chrome
authenticates the extension against the host manifest's single
`allowed_origins` entry; the native host also checks Chrome's origin argument.
The native host reads the ACL-protected router token itself and never returns it
to the extension.

Publication is not release-ready until `scripts/verify_chrome_connector.ps1`
passes with a published ID and a signed host binary in a clean Windows VM. The
current host stages bounded local context records; the release gate also
requires the desktop to consume one of those records end to end.

Permission rationale and the user-data contract are documented in
`PRIVACY.md`. Draft listing copy is in `STORE-LISTING.md`; neither file is proof
that the extension has been reviewed or published by Chrome Web Store.

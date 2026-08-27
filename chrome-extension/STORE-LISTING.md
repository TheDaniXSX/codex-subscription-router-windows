# Chrome Web Store listing draft

## Short description

Share the current web page with your local Codex Subscription Router on
Windows, only when you explicitly request it.

## Detailed description

Codex Subscription Router Connector sends the active HTTP(S) page's URL, title,
selected text, and bounded visible text to a router-owned Native Messaging host
on the same Windows account. It does not run on every page and has no broad host
permission. Capture occurs only after clicking **Share this page**.

The extension requires the independently installed Codex Subscription Router
Chrome host. It neither installs nor modifies the official Codex browser
connector. Source, permission rationale, retention behavior, and uninstall
instructions are available in the project repository.

## Single-purpose statement

The extension's sole purpose is user-initiated transfer of the current page's
context to Codex Subscription Router running locally on Windows.

## Publication checklist

- Replace this draft with final localized listing copy.
- Provide project-owned 16, 32, 48 and 128 pixel icons and store artwork.
- Link the published privacy policy.
- Record the Chrome Web Store extension ID in release qualification evidence.
- Run the signed-host clean-VM gate against that exact published version.

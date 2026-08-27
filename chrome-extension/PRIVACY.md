# Privacy disclosure

Codex Subscription Router Connector has no developer-operated network service
and does not send browser data to the project maintainer.

The extension requests only:

- `activeTab`, so access is limited to the current tab after a user gesture;
- `scripting`, to read the selected text and visible body text after the user
  clicks **Share this page**;
- `nativeMessaging`, to send that context to the separately installed local
  router-owned host.

On that explicit action it can collect the active HTTP(S) page URL, title,
selection, and up to 256 KiB of visible text. The native host stores the record
under the router's ACL-protected local state and retains at most 20 records. It
does not capture password-manager fields, cookies, browsing history, other
tabs, `file:` pages, or browser-internal pages.

The extension never receives the router control token. Chrome authenticates
its ID through `allowed_origins`; the host validates the caller origin again
and uses the token only for the loopback request.

Users can remove the extension and its native host independently. Router state,
including staged contexts, is preserved by default so uninstall is recoverable;
delete the `chrome-connector/contexts` directory under the configured router
state root only after confirming that no saved context is needed.

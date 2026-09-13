# Opening router histories in the official app

Per-request routing stores the provider ID `codex_router_spend` in thread
metadata. The router supplies that provider with process-local `-c` overrides.
The official app does not inherit those overrides, so resuming a routed history
can fail with `Model provider codex_router_spend not found` even when the shared
`config.toml` is valid.

Register a direct OpenAI compatibility alias in the user config for the home
that owns the history. Do not copy the router's localhost URL or token into it.
The default provider and all histories remain unchanged. Outside the router,
the alias uses the official app's own signed-in account and native default
endpoint. Auto/selected-subscription spending applies **only inside the router**.
The router continues overriding the alias's URL, auth header and transport
settings in its own child processes.

With Python 3.11 or later, run from the repository (replace the example path
with the actual config used by the official app):

```powershell
python scripts/repair_provider_compatibility.py --config 'C:\Users\YOUR_USER\.codex\config.toml'
python scripts/repair_provider_compatibility.py --config 'C:\Users\YOUR_USER\.codex\config.toml' --apply
```

The first command previews whether a repair is needed. The second preserves the
original config bytes in a uniquely named adjacent `.bak`, validates the TOML,
adds only the provider table, and leaves an already-compatible file unchanged.
Conflicting existing provider definitions are rejected instead of overwritten.
Do not edit settings concurrently with the repair. Reopen the affected task
after saving; if the app still holds stale config, restart it when idle.

To revert, remove only the added `model_providers.codex_router_spend` table and
its two comments, or restore the reported backup if no later settings need to
be retained. Removing the alias makes routed histories unavailable to the
official app again; it does not delete them.

Official configuration guidance:
[Custom model providers](https://learn.chatgpt.com/docs/config-file/config-advanced).

const CODEX_MUX_API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1";
const CODEX_MUX_TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";
let codexMuxLoginActive = false;
let codexMuxPendingLogin = null;

const CODEX_MUX_ACCOUNT_LABEL_MAX_LENGTH = 80;
const CODEX_MUX_ACCOUNT_STATUSES = new Set([
  "ready",
  "pending",
  "disconnected",
  "disabled",
  "restarting",
  "error",
]);

function codexMuxAccountStatus(account, pendingAccountId = null) {
  let status = CODEX_MUX_ACCOUNT_STATUSES.has(account?.status)
    ? account.status
    : null;
  if (!account?.enabled) status = "disabled";
  else if (pendingAccountId === account?.id) status = "pending";
  else if (!status && account?.connected) status = "ready";
  else if (!status && account?.error) status = "error";
  else if (!status) status = "disconnected";

  const descriptions = {
    ready: "Connected",
    pending: "Sign-in pending",
    disconnected: "Disconnected",
    disabled: "Disabled",
    restarting: "Reconnecting…",
    error: "Connection error",
  };
  return {
    key: status,
    label: descriptions[status],
    canLogin: status === "disconnected" || status === "error",
    canCancelLogin: status === "pending",
    canLogout: status === "ready",
    canToggle: status !== "pending" && status !== "restarting",
  };
}

function codexMuxPendingLoginSettled(account) {
  if (!account) return true;
  const status = codexMuxAccountStatus(account, null).key;
  return status !== "pending" && status !== "restarting";
}

function codexMuxValidateAccountLabel(value) {
  const label = typeof value === "string" ? value.trim() : "";
  if (!label) throw new Error("Subscription name cannot be empty.");
  if (label.length > CODEX_MUX_ACCOUNT_LABEL_MAX_LENGTH) {
    throw new Error(
      `Subscription name must be ${CODEX_MUX_ACCOUNT_LABEL_MAX_LENGTH} characters or fewer.`,
    );
  }
  return label;
}

function codexMuxAccountPath(accountId, suffix = "") {
  return `/accounts/${encodeURIComponent(accountId)}${suffix}`;
}

function codexMuxRememberPendingLogin(login) {
  codexMuxPendingLogin = login || null;
  codexMuxLoginActive = codexMuxPendingLogin != null;
  return codexMuxPendingLogin;
}

async function codexMuxPatchAccount(accountId, patch) {
  return codexMuxRequest(codexMuxAccountPath(accountId), {
    method: "PATCH",
    body: JSON.stringify(patch),
  });
}

async function codexMuxStartAccountLogin(accountId) {
  if (codexMuxPendingLogin) {
    throw new Error(
      "Finish or cancel the current subscription sign-in before starting another.",
    );
  }
  const result = await codexMuxRequest(
    codexMuxAccountPath(accountId, "/login"),
    {
      method: "POST",
      body: JSON.stringify({ mode: "chatgptDeviceCode" }),
    },
  );
  if (
    !result.login ||
    typeof result.login.loginId !== "string" ||
    result.login.loginId.trim() === ""
  ) {
    throw new Error("The sign-in service returned no login identifier.");
  }
  return codexMuxRememberPendingLogin({ ...result.login, accountId });
}

async function codexMuxCancelAccountLogin(accountId) {
  const result = await codexMuxRequest(
    codexMuxAccountPath(accountId, "/login/cancel"),
    { method: "POST", body: "{}" },
  );
  if (codexMuxPendingLogin?.accountId === accountId) {
    codexMuxRememberPendingLogin(null);
  }
  return result;
}

async function codexMuxLogoutAccount(accountId) {
  return codexMuxRequest(codexMuxAccountPath(accountId, "/logout"), {
    method: "POST",
    body: "{}",
  });
}

async function codexMuxDeleteAccount(account) {
  if (!account || account.controller || account.id === "primary") {
    throw new Error("The primary subscription cannot be removed.");
  }
  return codexMuxRequest(codexMuxAccountPath(account.id), {
    method: "DELETE",
  });
}

function CodexMuxProfileMenuOpenChange(setOpen) {
  return (nextOpen) => {
    if (!nextOpen && codexMuxLoginActive) return;
    setOpen(nextOpen);
  };
}

async function codexMuxRequest(path, options = {}) {
  const response = await fetch(`${CODEX_MUX_API}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-Codex-Mux-Token": CODEX_MUX_TOKEN,
      ...options.headers,
    },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
  return body;
}

const CODEX_MUX_SSE_MIN_RETRY_MS = 1_000;
const CODEX_MUX_SSE_MAX_RETRY_MS = 30_000;
const CODEX_MUX_SSE_MAX_EVENT_BYTES = 256 * 1024;
const CODEX_MUX_UTF8_ENCODER = new TextEncoder();

function codexMuxUtf8ByteLength(value) {
  return CODEX_MUX_UTF8_ENCODER.encode(value).byteLength;
}

function codexMuxSubscribeEvents({ apiBase, token, onMessage }) {
  const controller = new AbortController();
  let stopped = false;
  let reconnectTimer = null;
  let reconnectDelay = CODEX_MUX_SSE_MIN_RETRY_MS;

  const waitToReconnect = () =>
    new Promise((resolve) => {
      if (stopped) {
        resolve(false);
        return;
      }
      const finish = (shouldReconnect) => {
        if (reconnectTimer !== null) {
          clearTimeout(reconnectTimer);
          reconnectTimer = null;
        }
        controller.signal.removeEventListener("abort", aborted);
        resolve(shouldReconnect);
      };
      const aborted = () => finish(false);
      controller.signal.addEventListener("abort", aborted, { once: true });
      reconnectTimer = setTimeout(() => finish(!stopped), reconnectDelay);
    });

  const run = async () => {
    while (!stopped) {
      try {
        const response = await fetch(`${apiBase}/events`, {
          method: "GET",
          headers: {
            Accept: "text/event-stream",
            "X-Codex-Mux-Token": token,
          },
          cache: "no-store",
          credentials: "omit",
          signal: controller.signal,
        });
        if (response.status === 204) return;
        if (!response.ok) {
          throw new Error(`Event stream failed (${response.status})`);
        }
        const contentType = response.headers.get("Content-Type") || "";
        if (!contentType.toLowerCase().startsWith("text/event-stream")) {
          throw new Error("Event stream returned an invalid content type");
        }
        if (!response.body || typeof response.body.getReader !== "function") {
          throw new Error("Event stream response is not readable");
        }
        reconnectDelay = CODEX_MUX_SSE_MIN_RETRY_MS;
        await codexMuxReadEventStream(
          response.body,
          onMessage,
          controller.signal,
          (retryMilliseconds) => {
            reconnectDelay = Math.min(
              CODEX_MUX_SSE_MAX_RETRY_MS,
              Math.max(CODEX_MUX_SSE_MIN_RETRY_MS, retryMilliseconds),
            );
          },
        );
      } catch (streamError) {
        if (stopped || streamError?.name === "AbortError") return;
        reconnectDelay = Math.min(
          CODEX_MUX_SSE_MAX_RETRY_MS,
          reconnectDelay * 2,
        );
      }
      if (!(await waitToReconnect())) return;
    }
  };

  void run();
  return () => {
    if (stopped) return;
    stopped = true;
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    controller.abort();
  };
}

async function codexMuxReadEventStream(body, onMessage, signal, onRetry) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let firstChunk = true;
  let dataLines = [];
  let eventDataBytes = 0;
  let eventType = "";
  let lastEventId = "";
  const cancelReader = () => {
    try {
      Promise.resolve(reader.cancel?.()).catch(() => {});
    } catch {}
  };
  if (signal.aborted) cancelReader();
  else signal.addEventListener("abort", cancelReader, { once: true });

  const dispatch = () => {
    if (dataLines.length > 0 && (eventType === "" || eventType === "message")) {
      try {
        onMessage({
          data: dataLines.join("\n"),
          lastEventId,
          type: eventType || "message",
        });
      } catch {}
    }
    dataLines = [];
    eventDataBytes = 0;
    eventType = "";
  };

  const processLine = (line) => {
    if (line === "") {
      dispatch();
      return;
    }
    if (line.startsWith(":")) return;
    const separator = line.indexOf(":");
    const field = separator === -1 ? line : line.slice(0, separator);
    let value = separator === -1 ? "" : line.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "data":
        eventDataBytes += codexMuxUtf8ByteLength(value);
        if (dataLines.length > 0) eventDataBytes += 1;
        if (eventDataBytes > CODEX_MUX_SSE_MAX_EVENT_BYTES) {
          throw new Error("Event stream message is too large");
        }
        dataLines.push(value);
        break;
      case "event":
        eventType = value;
        break;
      case "id":
        if (!value.includes("\0")) lastEventId = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) onRetry(Number(value));
        break;
    }
  };

  const consumeCompleteLines = (atEnd = false) => {
    let offset = 0;
    for (let index = 0; index < buffer.length; index += 1) {
      const character = buffer[index];
      if (character !== "\r" && character !== "\n") continue;
      if (character === "\r" && index + 1 === buffer.length && !atEnd) break;
      processLine(buffer.slice(offset, index));
      if (character === "\r" && buffer[index + 1] === "\n") index += 1;
      offset = index + 1;
    }
    buffer = buffer.slice(offset);
  };

  try {
    while (!signal.aborted) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      if (firstChunk && buffer.length > 0) {
        firstChunk = false;
        if (buffer.startsWith("\uFEFF")) buffer = buffer.slice(1);
      }
      consumeCompleteLines();
      if (codexMuxUtf8ByteLength(buffer) > CODEX_MUX_SSE_MAX_EVENT_BYTES) {
        throw new Error("Event stream line is too large");
      }
    }
    buffer += decoder.decode();
    consumeCompleteLines(true);
  } finally {
    signal.removeEventListener("abort", cancelReader);
    reader.releaseLock?.();
  }
}

const CODEX_MUX_ACCOUNT_SCOPED_PLUGIN_METHODS = new Set([
  "list-apps",
  "list-installed-apps",
  "read-apps",
  "list-mcp-server-status",
  "login-mcp-server",
]);

function codexMuxScopePluginRequest(method, params) {
  const accountId = globalThis.__codexMuxPluginAccountId;
  if (
    !accountId ||
    !CODEX_MUX_ACCOUNT_SCOPED_PLUGIN_METHODS.has(method) ||
    (params != null &&
      (typeof params !== "object" || Array.isArray(params)))
  ) {
    return params;
  }
  return { ...(params || {}), codexMuxAccountId: accountId };
}

async function codexMuxProfileData(accountId = null) {
  const query = accountId
    ? `?accountId=${encodeURIComponent(accountId)}`
    : "";
  const result = await codexMuxRequest(`/profile/combined${query}`);
  globalThis.__codexMuxCombinedProfileAccounts = result.accounts || [];
  return result.profile;
}

async function codexMuxRateLimitResets(accountId) {
  return codexMuxRequest(
    `/accounts/${encodeURIComponent(accountId)}/rate-limit-resets`,
  );
}

async function codexMuxConsumeRateLimitReset(accountId, input) {
  return codexMuxRequest(
    `/accounts/${encodeURIComponent(accountId)}/rate-limit-resets/consume`,
    {
      method: "POST",
      body: JSON.stringify({
        creditId: input.creditId ?? null,
        redeemRequestId: input.redeemRequestId,
      }),
    },
  );
}

function CodexMuxUsageModal({
  onClose,
}) {
  return (0, e7.jsx)(QLs, {
    defaultResetCreditsOpen: true,
    initialAvailableCount: 0,
    isRateLimitReached: false,
    onClose,
    onResetComplete: () => {},
  });
}

function CodexMuxUseResetAccountState() {
  const cachedAccounts = (globalThis.__codexMuxConnectedAccounts || []).filter(
    (account) => account.connected && account.enabled,
  );
  const [accounts, setAccounts] = kXc.useState(cachedAccounts);
  const [selectedId, setSelectedId] = kXc.useState("primary");
  const [resetCounts, setResetCounts] = kXc.useState({});
  const [loading, setLoading] = kXc.useState(cachedAccounts.length === 0);

  const loadAccounts = kXc.useCallback(async () => {
    const result = await codexMuxRequest("/accounts");
    const connected = (result.accounts || []).filter(
      (account) => account.connected && account.enabled,
    );
    setAccounts(connected);
    setSelectedId((current) =>
      connected.some((account) => account.id === current)
        ? current
        : connected[0]?.id || "primary",
    );
    setLoading(false);
    const entries = await Promise.all(
      connected.map(async (account) => {
        try {
          const resets = await codexMuxRateLimitResets(account.id);
          return [account.id, Math.max(0, resets.available_count || 0)];
        } catch {
          return [account.id, null];
        }
      }),
    );
    setResetCounts(Object.fromEntries(entries));
  }, []);

  kXc.useEffect(() => {
    loadAccounts().catch(() => setLoading(false));
  }, [loadAccounts]);

  kXc.useEffect(
    () => () => {
      delete window.__codexMuxResetAccountId;
      delete window.__codexMuxSelectedUsageWindows;
      delete window.__codexMuxResetAccountSelector;
    },
    [],
  );

  const selected =
    accounts.find((account) => account.id === selectedId) || accounts[0] || null;
  const activeId = selected?.id || selectedId;
  window.__codexMuxResetAccountId = activeId;
  window.__codexMuxSelectedUsageWindows = selected
    ? codexMuxUsageWindows(selected.rateLimits)
    : null;
  window.__codexMuxResetAccountSelector = (0, e7.jsx)(
    CodexMuxResetAccountSelector,
    {
      accounts,
      loading,
      resetCounts,
      selectedId: activeId,
      onSelect: setSelectedId,
    },
  );

}

function CodexMuxResetAccountSelector({
  accounts,
  loading,
  onSelect,
  resetCounts,
  selectedId,
}) {
  return (0, e7.jsxs)("div", {
    className: "pt-4",
    children: [
      (0, e7.jsx)("div", {
        className:
          "mb-2 px-1 text-xs font-medium text-token-text-secondary",
        children: "Subscription",
      }),
      (0, e7.jsx)("div", {
        className:
          "flex flex-wrap gap-2 rounded-2xl border border-token-border p-2",
        role: "group",
        "aria-label": "Subscription",
        "data-codex-mux-action": "select-reset-subscription",
        children: loading
          ? (0, e7.jsx)("div", {
              className: "px-2 py-2 text-sm text-token-text-secondary",
              children: "Loading subscriptions…",
            })
          : accounts.map((account) => {
              const selected = account.id === selectedId;
              const count = resetCounts[account.id];
              return (0, e7.jsxs)(
                "button",
                {
                  type: "button",
                  className: [
                    "flex min-w-fit items-center gap-2 rounded-xl px-3 py-2 text-left",
                    "transition-colors hover:bg-token-foreground/5",
                    selected
                      ? "bg-token-foreground/10 text-token-text-primary"
                      : "text-token-text-secondary",
                  ].join(" "),
                  "aria-pressed": selected,
                  "aria-label": `Use ${account.label} for usage resets`,
                  onClick: () => onSelect(account.id),
                  children: [
                    (0, e7.jsx)(CodexMuxAccountAvatar, {
                      imageUrl: account.profileImageUrl,
                      label: account.label,
                      className: "size-7",
                    }),
                    (0, e7.jsxs)("span", {
                      className: "flex min-w-0 flex-col",
                      children: [
                        (0, e7.jsx)("span", {
                          className: "max-w-40 truncate text-sm font-medium",
                          children: account.planLabel
                            ? `${account.label} · ${account.planLabel}`
                            : account.label,
                        }),
                        (0, e7.jsx)("span", {
                          className: "text-xs text-token-text-tertiary",
                          children:
                            count == null
                              ? "Resets unavailable"
                              : count === 1
                                ? "1 reset available"
                                : `${count} resets available`,
                        }),
                      ],
                    }),
                  ],
                },
                account.id,
              );
            }),
      }),
    ],
  });
}

function CodexMuxAccountMenu() {
  const modalScope = Lo(Q);
  const [accounts, setAccounts] = kXc.useState([]);
  const [loading, setLoading] = kXc.useState(true);
  const [busy, setBusy] = kXc.useState("");
  const [error, setError] = kXc.useState("");
  const [statusMessage, setStatusMessage] = kXc.useState("");
  const [login, setLogin] = kXc.useState(() => codexMuxPendingLogin);
  const [codeCopied, setCodeCopied] = kXc.useState(false);
  const [selectedAccountId, setSelectedAccountId] = kXc.useState(
    codexMuxPendingLogin?.accountId || null,
  );
  const loginAccountId = login?.accountId || null;

  const refresh = kXc.useCallback(async () => {
    try {
      const result = await codexMuxRequest("/accounts");
      const nextAccounts = result.accounts || [];
      globalThis.__codexMuxConnectedAccounts = nextAccounts.filter(
        (account) => account.connected && account.enabled,
      );
      setAccounts(nextAccounts);
      setSelectedAccountId((current) =>
        nextAccounts.some((account) => account.id === current)
          ? current
          : null,
      );
      setLoading(false);
      return nextAccounts;
    } catch (requestError) {
      setError(requestError.message);
      setLoading(false);
      return null;
    }
  }, []);

  const clearPendingLogin = kXc.useCallback(() => {
    codexMuxRememberPendingLogin(null);
    setLogin(null);
    setCodeCopied(false);
  }, []);

  kXc.useEffect(() => {
    void refresh();
    const stopEvents = codexMuxSubscribeEvents({
      apiBase: CODEX_MUX_API,
      token: CODEX_MUX_TOKEN,
      onMessage: (event) => {
        try {
          const payload = JSON.parse(event.data);
          if (payload.type !== "account-updated") return;
          void refresh().then((nextAccounts) => {
            if (payload.accountId !== loginAccountId) return;
            if (nextAccounts == null) return;
            const updated = nextAccounts.find(
              (account) => account.id === loginAccountId,
            );
            if (!codexMuxPendingLoginSettled(updated)) return;
            const nextStatus = updated
              ? codexMuxAccountStatus(updated, null).key
              : "disconnected";
            clearPendingLogin();
            if (nextStatus === "ready") {
              setStatusMessage("Subscription connected.");
              setError("");
            } else if (updated?.error) {
              setError(updated.error);
            }
          });
        } catch {}
      },
    });
    const warmupTimer = setTimeout(refresh, 2_000);
    const loadingDeadline = setTimeout(() => {
      refresh().finally(() => setLoading(false));
    }, 6_000);
    const timer = setInterval(refresh, 30_000);
    return () => {
      clearTimeout(warmupTimer);
      clearTimeout(loadingDeadline);
      clearInterval(timer);
      stopEvents();
    };
  }, [clearPendingLogin, refresh, loginAccountId]);

  const connected = accounts.filter(
    (account) => account.connected && account.enabled,
  );
  const weeklyWindows = connected.map((account) =>
    codexMuxWeeklyWindow(account.rateLimits),
  );
  const hasCompleteUsage =
    connected.length > 0 && weeklyWindows.every((weekly) => weekly != null);
  const totalRemaining = weeklyWindows.reduce(
    (total, weekly) =>
      total + (weekly == null ? 0 : Math.max(0, 100 - weekly.usedPercent)),
    0,
  );

  const selectedAccount =
    accounts.find((account) => account.id === selectedAccountId) || null;
  const backendPendingAccount = accounts.find(
    (account) => account.status === "pending",
  );

  function keepMenuOpen(event) {
    event?.preventDefault?.();
  }

  async function runAccountAction(action, operation, successMessage) {
    if (busy) return false;
    setBusy(action);
    setError("");
    setStatusMessage("");
    try {
      await operation();
      if (successMessage) setStatusMessage(successMessage);
      await refresh();
      return true;
    } catch (requestError) {
      setError(requestError?.message || "The subscription action failed.");
      return false;
    } finally {
      setBusy("");
    }
  }

  async function addSubscription(event) {
    keepMenuOpen(event);
    if (busy) return;
    if (codexMuxPendingLogin || backendPendingAccount) {
      setError(
        "Finish or cancel the current subscription sign-in before adding another.",
      );
      return;
    }
    await runAccountAction("add", async () => {
      const created = await codexMuxRequest("/accounts", {
        method: "POST",
        body: JSON.stringify({ label: `Subscription ${accounts.length + 1}` }),
      });
      setSelectedAccountId(created.account.id);
      const pendingLogin = await codexMuxStartAccountLogin(created.account.id);
      setCodeCopied(false);
      setLogin(pendingLogin);
      setStatusMessage("Subscription created. Complete sign-in to connect it.");
    });
  }

  async function startLogin(account, event) {
    keepMenuOpen(event);
    if (backendPendingAccount) {
      setError(
        "Finish or cancel the current subscription sign-in before starting another.",
      );
      return;
    }
    await runAccountAction(`login:${account.id}`, async () => {
      const pendingLogin = await codexMuxStartAccountLogin(account.id);
      setLogin(pendingLogin);
      setSelectedAccountId(account.id);
      setCodeCopied(false);
    }, "Sign-in started. Copy the code and continue in your browser.");
  }

  async function cancelLogin(account, event) {
    keepMenuOpen(event);
    const cancelled = await runAccountAction(
      `cancel:${account.id}`,
      () => codexMuxCancelAccountLogin(account.id),
      "Sign-in cancelled. The subscription remains available to retry.",
    );
    if (cancelled) clearPendingLogin();
  }

  async function renameAccount(account, event) {
    keepMenuOpen(event);
    const proposed = window.prompt("Subscription name", account.label);
    if (proposed == null) return;
    let label;
    try {
      label = codexMuxValidateAccountLabel(proposed);
    } catch (validationError) {
      setError(validationError.message);
      return;
    }
    await runAccountAction(
      `rename:${account.id}`,
      () => codexMuxPatchAccount(account.id, { label }),
      `Renamed subscription to ${label}.`,
    );
  }

  async function toggleAccount(account, event) {
    keepMenuOpen(event);
    const enabled = !account.enabled;
    await runAccountAction(
      `toggle:${account.id}`,
      () => codexMuxPatchAccount(account.id, { enabled }),
      enabled ? "Subscription enabled." : "Subscription disabled.",
    );
  }

  async function logoutAccount(account, event) {
    keepMenuOpen(event);
    if (
      !window.confirm(
        `Sign out of ${account.label}? Existing local conversation assignments will be kept.`,
      )
    ) {
      return;
    }
    await runAccountAction(
      `logout:${account.id}`,
      () => codexMuxLogoutAccount(account.id),
      "Subscription signed out.",
    );
  }

  async function deleteAccount(account, event) {
    keepMenuOpen(event);
    if (account.controller || account.id === "primary") {
      setError("The primary subscription cannot be removed.");
      return;
    }
    if (
      !window.confirm(
        `Remove ${account.label}? Its isolated sign-in data and local thread assignments will be deleted from this router.`,
      )
    ) {
      return;
    }
    const deleted = await runAccountAction(
      `delete:${account.id}`,
      () => codexMuxDeleteAccount(account),
      "Secondary subscription removed.",
    );
    if (!deleted) return;
    if (loginAccountId === account.id) clearPendingLogin();
    setSelectedAccountId(null);
  }

  async function copyCodeAndContinue(event) {
    keepMenuOpen(event);
    const userCode = login?.userCode || "";
    const verificationUrl = login?.verificationUrl || login?.authUrl || "";
    const copy = userCode
      ? navigator.clipboard.writeText(userCode)
      : Promise.resolve();
    if (verificationUrl) {
      try {
        const destination = new URL(verificationUrl);
        const trustedHost =
          destination.hostname === "chatgpt.com" ||
          destination.hostname === "auth.openai.com";
        if (destination.protocol !== "https:" || !trustedHost) {
          throw new Error("untrusted verification URL");
        }
        window.open(destination.href, "_blank", "noopener,noreferrer");
      } catch {
        setError("The sign-in verification page could not be opened safely.");
      }
    }
    try {
      await copy;
      setCodeCopied(userCode !== "");
    } catch {
      setError("The sign-in code could not be copied.");
    }
  }

  const rows = [];
  rows.push(
    (0, e7.jsx)(
      _H,
      {
        LeftIcon: S2,
        SubText: loading
          ? "Connecting subscriptions…"
          : connected.length === 1
            ? "1 connected subscription"
            : `${connected.length} connected subscriptions`,
        rightIcon: (0, e7.jsx)("span", {
          className: "text-token-description-foreground tabular-nums",
          children: loading
            ? "…"
            : hasCompleteUsage
              ? `${Math.round(totalRemaining)}%`
              : "–",
        }),
        onSelect: () => BW(modalScope, CodexMuxUsageModal, {}),
        children: "Usage remaining",
      },
      "codex-mux-total",
    ),
  );
  if (accounts.length > 0) {
    rows.push(
      (0, e7.jsx)(CH.Separator, {}, "codex-mux-accounts-separator"),
    );
  }

  for (const account of accounts) {
    const state = codexMuxAccountStatus(account, loginAccountId);
    const weekly = codexMuxWeeklyWindow(account.rateLimits);
    const remaining = weekly == null ? null : Math.max(0, 100 - weekly.usedPercent);
    const identity = account.email
      ? (0, e7.jsx)(CodexMuxMaskedEmail, { email: account.email })
      : account.planType || "ChatGPT subscription";
    const subText = (0, e7.jsxs)("span", {
      className: "flex min-w-0 flex-col",
      children: [
        (0, e7.jsx)("span", {
          className: "text-token-text-secondary",
          children: identity,
        }),
        (0, e7.jsx)("span", {
          className:
            state.key === "error"
              ? "text-red-500"
              : "text-token-text-tertiary",
          children:
            state.key === "error" && account.error
              ? `${state.label}: ${account.error}`
              : state.label,
        }),
      ],
    });
    rows.push(
      (0, e7.jsx)(
        _H,
        {
          LeftIcon: (iconProps) =>
            (0, e7.jsx)(CodexMuxAccountAvatar, {
              ...iconProps,
              imageUrl: account.profileImageUrl,
              label: account.label,
            }),
          SubText: subText,
          className: "group",
          "aria-label": `${account.label}, ${state.label}. Manage subscription`,
          "aria-current": selectedAccountId === account.id ? "true" : undefined,
          "data-codex-mux-account-id": account.id,
          "data-codex-mux-state": state.key,
          onSelect: (event) => {
            keepMenuOpen(event);
            setSelectedAccountId((current) =>
              current === account.id ? null : account.id,
            );
          },
          rightIcon: (0, e7.jsx)("span", {
            className: "text-token-description-foreground tabular-nums",
            children:
              state.key === "ready" && remaining != null
                ? `${Math.round(remaining)}%`
                : state.label,
          }),
          children: account.planLabel
            ? `${account.label} · ${account.planLabel}`
            : account.label,
        },
        `codex-mux-account-${account.id}`,
      ),
    );
  }

  if (selectedAccount) {
    const selectedState = codexMuxAccountStatus(
      selectedAccount,
      loginAccountId,
    );
    rows.push(
      (0, e7.jsx)(CH.Separator, {}, "codex-mux-manage-separator"),
    );
    rows.push(
      (0, e7.jsx)(
        _H,
        {
          LeftIcon: S2,
          SubText: selectedAccount.label,
          "aria-label": `Rename ${selectedAccount.label}`,
          "aria-busy": busy === `rename:${selectedAccount.id}`,
          "aria-disabled": busy !== "",
          "data-codex-mux-action": "rename",
          "data-codex-mux-account-id": selectedAccount.id,
          onSelect: (event) => renameAccount(selectedAccount, event),
          children: busy === `rename:${selectedAccount.id}`
            ? "Renaming…"
            : "Rename subscription",
        },
        `codex-mux-rename-${selectedAccount.id}`,
      ),
    );
    if (selectedState.canLogin) {
      rows.push(
        (0, e7.jsx)(
          _H,
          {
            LeftIcon: CodexMuxPlusIcon,
            SubText:
              selectedState.key === "error"
                ? "Retry the ChatGPT device sign-in"
                : "Connect this ChatGPT subscription",
            "aria-label": `Sign in to ${selectedAccount.label}`,
            "aria-busy": busy === `login:${selectedAccount.id}`,
            "aria-disabled": busy !== "",
            "data-codex-mux-action": "login",
            "data-codex-mux-account-id": selectedAccount.id,
            onSelect: (event) => startLogin(selectedAccount, event),
            children: busy === `login:${selectedAccount.id}`
              ? "Starting sign-in…"
              : selectedState.key === "error"
                ? "Retry sign-in"
                : "Sign in",
          },
          `codex-mux-sign-in-${selectedAccount.id}`,
        ),
      );
    }
    if (selectedState.canCancelLogin) {
      rows.push(
        (0, e7.jsx)(
          _H,
          {
            LeftIcon: S2,
            SubText: "Keep the account and stop this sign-in attempt",
            "aria-label": `Cancel sign-in for ${selectedAccount.label}`,
            "aria-busy": busy === `cancel:${selectedAccount.id}`,
            "aria-disabled": busy !== "",
            "data-codex-mux-action": "cancel-login",
            "data-codex-mux-account-id": selectedAccount.id,
            onSelect: (event) => cancelLogin(selectedAccount, event),
            children: busy === `cancel:${selectedAccount.id}`
              ? "Cancelling sign-in…"
              : "Cancel sign-in",
          },
          `codex-mux-cancel-${selectedAccount.id}`,
        ),
      );
    }
    if (selectedState.canLogout) {
      rows.push(
        (0, e7.jsx)(
          _H,
          {
            LeftIcon: S2,
            SubText: "Keep this subscription in the router",
            "aria-label": `Sign out of ${selectedAccount.label}`,
            "aria-busy": busy === `logout:${selectedAccount.id}`,
            "aria-disabled": busy !== "",
            "data-codex-mux-action": "logout",
            "data-codex-mux-account-id": selectedAccount.id,
            onSelect: (event) => logoutAccount(selectedAccount, event),
            children: busy === `logout:${selectedAccount.id}`
              ? "Signing out…"
              : "Sign out",
          },
          `codex-mux-logout-${selectedAccount.id}`,
        ),
      );
    }
    if (
      selectedState.canToggle &&
      (!selectedAccount.controller || !selectedAccount.enabled)
    ) {
      rows.push(
        (0, e7.jsx)(
          _H,
          {
            LeftIcon: S2,
            SubText: selectedAccount.enabled
              ? "Stop routing new work to this subscription"
              : "Allow this subscription to reconnect and receive work",
            "aria-label": `${selectedAccount.enabled ? "Disable" : "Enable"} ${selectedAccount.label}`,
            "aria-busy": busy === `toggle:${selectedAccount.id}`,
            "aria-disabled": busy !== "",
            "data-codex-mux-action": selectedAccount.enabled
              ? "disable"
              : "enable",
            "data-codex-mux-account-id": selectedAccount.id,
            onSelect: (event) => toggleAccount(selectedAccount, event),
            children: busy === `toggle:${selectedAccount.id}`
              ? "Updating…"
              : selectedAccount.enabled
                ? "Disable subscription"
                : "Enable subscription",
          },
          `codex-mux-toggle-${selectedAccount.id}`,
        ),
      );
    }
    if (
      !selectedAccount.controller &&
      selectedAccount.id !== "primary" &&
      selectedState.key !== "pending" &&
      selectedState.key !== "restarting"
    ) {
      rows.push(
        (0, e7.jsx)(
          _H,
          {
            LeftIcon: S2,
            SubText: "Permanently remove its isolated local data",
            tone: "danger",
            "aria-label": `Remove ${selectedAccount.label}`,
            "aria-busy": busy === `delete:${selectedAccount.id}`,
            "aria-disabled": busy !== "",
            "data-codex-mux-action": "delete",
            "data-codex-mux-account-id": selectedAccount.id,
            onSelect: (event) => deleteAccount(selectedAccount, event),
            children: busy === `delete:${selectedAccount.id}`
              ? "Removing…"
              : "Remove subscription",
          },
          `codex-mux-delete-${selectedAccount.id}`,
        ),
      );
    }
  }

  if (login) {
    rows.push(
      (0, e7.jsx)(
        _H,
        {
          LeftIcon: CodexMuxCopyIcon,
          SubText: login.userCode
            ? codeCopied
              ? `Code ${login.userCode} copied`
              : `Code ${login.userCode} · Click to copy`
            : "Finish signing in with ChatGPT",
          onSelect: copyCodeAndContinue,
          "aria-label": login.userCode
            ? `Copy sign-in code ${login.userCode} and open ChatGPT`
            : "Continue subscription sign-in in ChatGPT",
          "data-codex-mux-action": "continue-login",
          "data-codex-mux-account-id": login.accountId,
          children: "Continue sign-in",
        },
        "codex-mux-login",
      ),
    );
  }

  if (error) {
    rows.push(
      (0, e7.jsx)(
        "div",
        {
          role: "alert",
          "aria-live": "assertive",
          className:
            "mx-2 my-1 rounded-lg bg-red-500/10 px-3 py-2 text-sm text-red-600",
          "data-codex-mux-state": "error",
          children: error,
        },
        "codex-mux-error",
      ),
    );
  }

  if (statusMessage) {
    rows.push(
      (0, e7.jsx)(
        "div",
        {
          role: "status",
          "aria-live": "polite",
          className:
            "mx-2 my-1 rounded-lg bg-token-foreground/5 px-3 py-2 text-sm text-token-text-secondary",
          "data-codex-mux-state": "status",
          children: statusMessage,
        },
        "codex-mux-status",
      ),
    );
  }

  if (!loading) {
    rows.push(
      (0, e7.jsx)(
        _H,
        {
          LeftIcon: CodexMuxPlusIcon,
          onSelect: addSubscription,
          "aria-label": "Add another ChatGPT subscription",
          "aria-busy": busy === "add",
          "aria-disabled":
            busy !== "" ||
            codexMuxPendingLogin != null ||
            backendPendingAccount != null,
          "data-codex-mux-action": "add",
          children: busy === "add"
            ? "Adding subscription…"
            : codexMuxPendingLogin || backendPendingAccount
              ? "Finish current sign-in first"
              : "Add another subscription",
        },
        "codex-mux-add",
      ),
    );
  }
  rows.push((0, e7.jsx)(CH.Separator, {}, "codex-mux-separator"));
  return (0, e7.jsx)(e7.Fragment, { children: rows });
}

function codexMuxWeeklyWindow(rateLimits) {
  const windows = [rateLimits?.primary, rateLimits?.secondary].filter(Boolean);
  windows.sort(
    (left, right) =>
      (left.windowDurationMins || 0) - (right.windowDurationMins || 0),
  );
  return windows.at(-1) || null;
}

function codexMuxUsageWindows(rateLimits) {
  return [rateLimits?.primary, rateLimits?.secondary]
    .filter(Boolean)
    .map((window) => ({
      usedPercent: window.usedPercent,
      remainingPercent: Math.max(0, 100 - window.usedPercent),
      windowMinutes: window.windowDurationMins || 0,
      resetsAt: window.resetsAt ?? null,
    }));
}

function CodexMuxPlusIcon(props) {
  return (0, e7.jsx)("svg", {
    viewBox: "0 0 20 20",
    fill: "none",
    "aria-hidden": true,
    ...props,
    children: (0, e7.jsx)("path", {
      d: "M10 4.25v11.5M4.25 10h11.5",
      stroke: "currentColor",
      strokeWidth: 1.5,
      strokeLinecap: "round",
    }),
  });
}

function CodexMuxCopyIcon(props) {
  return (0, e7.jsx)("svg", {
    viewBox: "0 0 20 20",
    fill: "none",
    "aria-hidden": true,
    ...props,
    children: (0, e7.jsxs)(e7.Fragment, {
      children: [
        (0, e7.jsx)("rect", {
          x: 6.25,
          y: 6.25,
          width: 9.5,
          height: 9.5,
          rx: 2,
          stroke: "currentColor",
          strokeWidth: 1.5,
        }),
        (0, e7.jsx)("path", {
          d: "M13.75 6.25V6A1.75 1.75 0 0 0 12 4.25H6A1.75 1.75 0 0 0 4.25 6v6c0 .97.78 1.75 1.75 1.75h.25",
          stroke: "currentColor",
          strokeWidth: 1.5,
          strokeLinecap: "round",
        }),
      ],
    }),
  });
}

function CodexMuxMaskedEmail({ email }) {
  return (0, e7.jsxs)(e7.Fragment, {
    children: [
      (0, e7.jsx)("span", {
        className: "group-hover:hidden",
        children: "••••••••",
      }),
      (0, e7.jsx)("span", {
        className: "hidden group-hover:inline",
        children: email,
      }),
    ],
  });
}

function CodexMuxAccountAvatar({ imageUrl, label, className }) {
  const [failed, setFailed] = kXc.useState(false);
  const resolvedImageUrl = jLa(imageUrl || null);
  if (resolvedImageUrl && !failed) {
    return (0, e7.jsx)("img", {
      src: resolvedImageUrl,
      alt: "",
      className: `${className || "icon-sm"} rounded-full object-cover`,
      referrerPolicy: "no-referrer",
      onError: () => setFailed(true),
    });
  }
  const initials = label
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("");
  return (0, e7.jsx)("span", {
    className: `${className || "icon-sm"} flex items-center justify-center rounded-full bg-token-charts-purple/10 text-[9px] leading-none text-token-charts-purple`,
    "aria-hidden": true,
    children: initials || "?",
  });
}

function CodexMuxOverlappingAvatars({ accounts, size = "size-20" }) {
  const overlapClass = size === "size-20" ? "-ml-10" : "-ml-2";
  return (0, e7.jsx)("div", {
    className: "flex items-center justify-center",
    children: accounts.map((account, index) =>
      (0, e7.jsx)(
        "span",
        {
          className: `${index === 0 ? "" : overlapClass} rounded-full border-4 border-token-bg-primary`,
          title: account.planLabel
            ? `${account.label} · ${account.planLabel}`
            : account.label,
          children: (0, e7.jsx)(CodexMuxAccountAvatar, {
            imageUrl: account.profileImageUrl,
            label: account.label,
            className: size,
          }),
        },
        account.id,
      ),
    ),
  });
}

function CodexMuxProfileAvatarStack({ onSelect }) {
  const [accounts, setAccounts] = kXc.useState(
    globalThis.__codexMuxCombinedProfileAccounts || [],
  );
  const [selectedId, setSelectedId] = kXc.useState(
    globalThis.__codexMuxSelectedProfileAccountId || null,
  );
  kXc.useEffect(() => {
    let live = true;
    codexMuxRequest("/accounts")
      .then((result) => {
        if (!live) return;
        const connected = (result.accounts || []).filter(
          (account) => account.connected && account.enabled,
        );
        globalThis.__codexMuxCombinedProfileAccounts = connected;
        setAccounts(connected);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);
  kXc.useEffect(() => {
    globalThis.__codexMuxSelectedProfileAccountId = null;
    setSelectedId(null);
    onSelect?.();
    return () => {
      globalThis.__codexMuxSelectedProfileAccountId = null;
    };
  }, []);
  if (accounts.length === 0) return null;
  const visibleAccounts = selectedId
    ? accounts.filter((account) => account.id === selectedId)
    : accounts;
  return (0, e7.jsx)("div", {
    className: "mb-4",
    "aria-label": selectedId
      ? "Selected subscription profile"
      : `${accounts.length} connected subscriptions`,
    children: (0, e7.jsx)("div", {
      className: "flex items-center justify-center",
      children: visibleAccounts.map((account, index) =>
        (0, e7.jsx)(
          "button",
          {
            type: "button",
            className: `${index === 0 ? "" : "-ml-5"} rounded-full border-4 border-token-bg-primary transition-transform hover:z-10 hover:scale-105 focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-token-focus-border`,
            style: {
              marginLeft: index === 0 ? 0 : -20,
              zIndex: index,
            },
            "aria-label": selectedId
              ? `Show combined profile stats`
              : `Show ${account.label} profile stats`,
            title: account.planLabel
              ? `${account.label} · ${account.planLabel}`
              : account.label,
            onClick: () => {
              const nextId = selectedId === account.id ? null : account.id;
              globalThis.__codexMuxSelectedProfileAccountId = nextId;
              setSelectedId(nextId);
              onSelect?.();
            },
            children: (0, e7.jsx)(CodexMuxAccountAvatar, {
              imageUrl: account.profileImageUrl,
              label: account.label,
              className: "size-20",
            }),
          },
          account.id,
        ),
      ),
    }),
  });
}

function CodexMuxPluginScope() {
  const [accounts, setAccounts] = kXc.useState([]);
  const [selectedId, setSelectedId] = kXc.useState("primary");
  const [loading, setLoading] = kXc.useState(true);
  const queryClient = lt();
  kXc.useEffect(() => {
    let live = true;
    codexMuxRequest("/accounts")
      .then((result) => {
        if (!live) return;
        setAccounts(
          (result.accounts || []).filter(
            (account) => account.connected && account.enabled,
          ),
        );
      })
      .catch(() => {})
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, []);

  kXc.useEffect(() => {
    globalThis.__codexMuxPluginAccountId = selectedId;
    return () => {
      delete globalThis.__codexMuxPluginAccountId;
    };
  }, [selectedId]);

  async function selectAccount(accountId) {
    if (accountId === selectedId) return;
    globalThis.__codexMuxPluginAccountId = accountId;
    setSelectedId(accountId);
    await queryClient.invalidateQueries({
      predicate: (query) => {
        const root = query.queryKey?.[0];
        return root === "apps" || root === "plugins" || root === "mcp";
      },
    });
  }

  const selected =
    accounts.find((account) => account.id === selectedId) || accounts[0] || null;

  return (0, e7.jsxs)("div", {
    className:
      "mb-5 rounded-2xl border border-token-border-light p-3",
    children: [
      (0, e7.jsxs)("div", {
        className: "px-1",
        children: [
          (0, e7.jsx)("div", {
            className: "text-sm font-medium text-token-text-primary",
            children: "Plugin connections",
          }),
          (0, e7.jsx)("div", {
            className: "mt-0.5 text-xs text-token-text-secondary",
            children: selected
              ? `Installs are shared. Connection access below is for ${selected.label}.`
              : "Installs are shared. Choose a subscription for connection access.",
          }),
        ],
      }),
      loading
        ? (0, e7.jsx)("div", {
            className: "mt-3 px-1 text-sm text-token-text-tertiary",
            children: "Loading subscriptions…",
          })
        : (0, e7.jsx)("div", {
            className: "mt-3 flex flex-wrap gap-2",
            children: accounts.map((account) => {
              const active = account.id === selected?.id;
              return (0, e7.jsxs)(
                "button",
                {
                  type: "button",
                  className: [
                    "flex items-center gap-2 rounded-xl px-2.5 py-2 text-sm transition-colors",
                    active
                      ? "bg-token-foreground/10 text-token-text-primary"
                      : "text-token-text-secondary hover:bg-token-foreground/5",
                  ].join(" "),
                  "aria-pressed": active,
                  onClick: () => selectAccount(account.id),
                  children: [
                    (0, e7.jsx)(CodexMuxAccountAvatar, {
                      imageUrl: account.profileImageUrl,
                      label: account.label,
                      className: "size-7",
                    }),
                    (0, e7.jsx)("span", {
                      children: account.planLabel
                        ? `${account.label} · ${account.planLabel}`
                        : account.label,
                    }),
                  ],
                },
                account.id,
              );
            }),
          }),
    ],
  });
}

// The thread summary is emitted into a separate lazy-loaded renderer chunk.
// Export the same avatar component so both surfaces share image resolution,
// error handling, and the initials fallback.
globalThis.CodexMuxAccountAvatar = CodexMuxAccountAvatar;
globalThis.codexMuxSubscribeEvents = codexMuxSubscribeEvents;
globalThis.codexMuxUtf8ByteLength = codexMuxUtf8ByteLength;
globalThis.codexMuxAccountStatus = codexMuxAccountStatus;
globalThis.codexMuxPendingLoginSettled = codexMuxPendingLoginSettled;
globalThis.codexMuxValidateAccountLabel = codexMuxValidateAccountLabel;
globalThis.codexMuxAccountPath = codexMuxAccountPath;
globalThis.codexMuxPatchAccount = codexMuxPatchAccount;
globalThis.codexMuxStartAccountLogin = codexMuxStartAccountLogin;
globalThis.codexMuxCancelAccountLogin = codexMuxCancelAccountLogin;
globalThis.codexMuxLogoutAccount = codexMuxLogoutAccount;
globalThis.codexMuxDeleteAccount = codexMuxDeleteAccount;
globalThis.codexMuxProfileData = codexMuxProfileData;
globalThis.CodexMuxProfileAvatarStack = (props) =>
  (0, e7.jsx)(CodexMuxProfileAvatarStack, props || {});
globalThis.CodexMuxPluginScope = () =>
  (0, e7.jsx)(CodexMuxPluginScope, {});

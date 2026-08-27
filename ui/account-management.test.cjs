const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const accountMenuPath = path.join(__dirname, "account-menu.js");
const accountMenuSource = fs.readFileSync(accountMenuPath, "utf8");

function loadAccountMenu(overrides = {}) {
  const context = vm.createContext({
    AbortController,
    TextDecoder,
    TextEncoder,
    clearInterval,
    clearTimeout,
    fetch,
    setInterval,
    setTimeout,
    ...overrides,
  });
  vm.runInContext(accountMenuSource, context, { filename: accountMenuPath });
  return context;
}

function jsonResponse(body, { ok = true, status = 200 } = {}) {
  return {
    ok,
    status,
    json: async () => body,
  };
}

test("account status exposes every backend lifecycle state", () => {
  const context = loadAccountMenu();
  const expected = {
    ready: ["Connected", false, false, true, true],
    pending: ["Sign-in pending", false, true, false, false],
    disconnected: ["Disconnected", true, false, false, true],
    disabled: ["Disabled", false, false, false, true],
    restarting: ["Reconnecting…", false, false, false, false],
    error: ["Connection error", true, false, false, true],
  };

  for (const [status, values] of Object.entries(expected)) {
    const actual = context.codexMuxAccountStatus({
      id: "secondary",
      enabled: status !== "disabled",
      connected: status === "ready",
      status,
    });
    assert.equal(actual.key, status);
    assert.equal(actual.label, values[0]);
    assert.equal(actual.canLogin, values[1]);
    assert.equal(actual.canCancelLogin, values[2]);
    assert.equal(actual.canLogout, values[3]);
    assert.equal(actual.canToggle, values[4]);
  }
});

test("pending login settles only after a conclusive backend state", () => {
  const context = loadAccountMenu();
  for (const status of ["pending", "restarting"]) {
    assert.equal(
      context.codexMuxPendingLoginSettled({
        id: "secondary",
        enabled: true,
        status,
      }),
      false,
    );
  }
  for (const status of ["ready", "disconnected", "disabled", "error"]) {
    assert.equal(
      context.codexMuxPendingLoginSettled({
        id: "secondary",
        enabled: status !== "disabled",
        status,
      }),
      true,
    );
  }
  assert.equal(context.codexMuxPendingLoginSettled(undefined), true);
});

test("account status has safe fallbacks and local pending state wins", () => {
  const context = loadAccountMenu();
  assert.equal(
    context.codexMuxAccountStatus({
      id: "secondary",
      enabled: true,
      connected: true,
      status: "future-value",
    }).key,
    "ready",
  );
  assert.equal(
    context.codexMuxAccountStatus({
      id: "secondary",
      enabled: true,
      connected: false,
      error: "backend unavailable",
    }).key,
    "error",
  );
  assert.equal(
    context.codexMuxAccountStatus(
      { id: "secondary", enabled: true, connected: false },
      "secondary",
    ).key,
    "pending",
  );
  assert.equal(
    context.codexMuxAccountStatus(
      { id: "secondary", enabled: false, connected: false },
      "secondary",
    ).key,
    "disabled",
  );
});

test("subscription labels are trimmed and bounded", () => {
  const context = loadAccountMenu();
  assert.equal(context.codexMuxValidateAccountLabel("  Personal  "), "Personal");
  assert.equal(context.codexMuxValidateAccountLabel("x".repeat(80)).length, 80);
  for (const invalid of ["", "   ", "x".repeat(81), null]) {
    assert.throws(
      () => context.codexMuxValidateAccountLabel(invalid),
      (error) => /cannot be empty|80 characters or fewer/.test(error.message),
    );
  }
});

test("account paths percent-encode hostile identifiers", () => {
  const context = loadAccountMenu();
  assert.equal(
    context.codexMuxAccountPath("secondary / ? # ü", "/login"),
    "/accounts/secondary%20%2F%20%3F%20%23%20%C3%BC/login",
  );
});

test("account API helpers use authenticated control endpoints and methods", async () => {
  const requests = [];
  const responses = [
    jsonResponse({ account: { id: "secondary" } }),
    jsonResponse({ ok: true }),
    jsonResponse({ ok: true }),
  ];
  const context = loadAccountMenu({
    fetch: async (url, options) => {
      requests.push({ url, options });
      return responses.shift();
    },
  });

  await context.codexMuxPatchAccount("secondary", {
    label: "Personal",
    enabled: false,
  });
  await context.codexMuxLogoutAccount("secondary");
  await context.codexMuxDeleteAccount({
    id: "secondary",
    controller: false,
  });

  assert.deepEqual(
    requests.map(({ url, options }) => ({
      path: url.replace(/^.*\/v1/, ""),
      method: options.method,
      body: options.body,
      token: options.headers["X-Codex-Mux-Token"],
    })),
    [
      {
        path: "/accounts/secondary",
        method: "PATCH",
        body: JSON.stringify({ label: "Personal", enabled: false }),
        token: "__CODEX_MUX_CONTROL_TOKEN__",
      },
      {
        path: "/accounts/secondary/logout",
        method: "POST",
        body: "{}",
        token: "__CODEX_MUX_CONTROL_TOKEN__",
      },
      {
        path: "/accounts/secondary",
        method: "DELETE",
        body: undefined,
        token: "__CODEX_MUX_CONTROL_TOKEN__",
      },
    ],
  );
});

test("primary/controller deletion is rejected before making a request", async () => {
  let fetchCount = 0;
  const context = loadAccountMenu({
    fetch: async () => {
      fetchCount += 1;
      return jsonResponse({ ok: true });
    },
  });

  await assert.rejects(
    context.codexMuxDeleteAccount({ id: "primary", controller: true }),
    /primary subscription cannot be removed/i,
  );
  await assert.rejects(
    context.codexMuxDeleteAccount({ id: "unexpected", controller: true }),
    /primary subscription cannot be removed/i,
  );
  assert.equal(fetchCount, 0);
});

test("only one device login may remain pending and cancellation releases it", async () => {
  const requests = [];
  const context = loadAccountMenu({
    fetch: async (url, options) => {
      requests.push({ url, options });
      if (url.endsWith("/login/cancel")) return jsonResponse({ ok: true });
      return jsonResponse({
        login: {
          loginId: "login-1",
          userCode: "ABCD-EFGH",
          verificationUrl: "https://auth.openai.com/device",
        },
      });
    },
  });

  const login = await context.codexMuxStartAccountLogin("secondary-1");
  assert.equal(login.accountId, "secondary-1");
  await assert.rejects(
    context.codexMuxStartAccountLogin("secondary-1"),
    /finish or cancel the current subscription sign-in/i,
  );
  await assert.rejects(
    context.codexMuxStartAccountLogin("secondary-2"),
    /finish or cancel the current subscription sign-in/i,
  );
  assert.equal(requests.length, 1);

  await context.codexMuxCancelAccountLogin("secondary-1");
  await context.codexMuxStartAccountLogin("secondary-2");
  assert.deepEqual(
    requests.map(({ url, options }) => ({
      path: url.replace(/^.*\/v1/, ""),
      method: options.method,
    })),
    [
      { path: "/accounts/secondary-1/login", method: "POST" },
      { path: "/accounts/secondary-1/login/cancel", method: "POST" },
      { path: "/accounts/secondary-2/login", method: "POST" },
    ],
  );
});

test("failed login can be retried because it never becomes pending", async () => {
  let attempt = 0;
  const context = loadAccountMenu({
    fetch: async () => {
      attempt += 1;
      if (attempt === 1) {
        return jsonResponse(
          { error: "Device authorization is temporarily unavailable." },
          { ok: false, status: 400 },
        );
      }
      return jsonResponse({
        login: { loginId: "login-2", userCode: "RETRY-OK" },
      });
    },
  });

  await assert.rejects(
    context.codexMuxStartAccountLogin("secondary"),
    /temporarily unavailable/i,
  );
  const retried = await context.codexMuxStartAccountLogin("secondary");
  assert.equal(retried.loginId, "login-2");
  assert.equal(attempt, 2);
});

test("malformed login response does not reserve the pending-login slot", async () => {
  let attempt = 0;
  const context = loadAccountMenu({
    fetch: async () => {
      attempt += 1;
      return attempt === 1
        ? jsonResponse({ login: { userCode: "NO-ID" } })
        : jsonResponse({
            login: { loginId: "valid-login", userCode: "HAS-ID" },
          });
    },
  });

  await assert.rejects(
    context.codexMuxStartAccountLogin("secondary"),
    /returned no login identifier/i,
  );
  const retried = await context.codexMuxStartAccountLogin("secondary");
  assert.equal(retried.loginId, "valid-login");
  assert.equal(attempt, 2);
});

test("failed cancellation keeps the global pending-login guard", async () => {
  let requestCount = 0;
  const context = loadAccountMenu({
    fetch: async (url) => {
      requestCount += 1;
      if (url.endsWith("/login/cancel")) {
        return jsonResponse(
          { error: "The sign-in attempt could not be cancelled." },
          { ok: false, status: 400 },
        );
      }
      return jsonResponse({
        login: { loginId: "pending-login", userCode: "WAIT-NOW" },
      });
    },
  });

  await context.codexMuxStartAccountLogin("secondary-1");
  await assert.rejects(
    context.codexMuxCancelAccountLogin("secondary-1"),
    /could not be cancelled/i,
  );
  await assert.rejects(
    context.codexMuxStartAccountLogin("secondary-2"),
    /finish or cancel the current subscription sign-in/i,
  );
  assert.equal(requestCount, 2);
});

test("control API errors remain actionable", async () => {
  const context = loadAccountMenu({
    fetch: async () =>
      jsonResponse(
        { error: "The last enabled subscription cannot be disabled." },
        { ok: false, status: 400 },
      ),
  });
  await assert.rejects(
    context.codexMuxPatchAccount("primary", { enabled: false }),
    /last enabled subscription cannot be disabled/i,
  );
});

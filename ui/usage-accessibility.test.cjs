const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const accountMenuPath = path.join(__dirname, "account-menu.js");
const accountMenuSource = fs.readFileSync(accountMenuPath, "utf8");

function jsx(type, props, key) {
  return { type, props: props || {}, key: key ?? null };
}

function loadAccountMenu(overrides = {}) {
  const context = vm.createContext({
    AbortController,
    TextDecoder,
    TextEncoder,
    clearInterval,
    clearTimeout,
    e7: { Fragment: Symbol("Fragment"), jsx, jsxs: jsx },
    fetch,
    setInterval,
    setTimeout,
    ...overrides,
  });
  vm.runInContext(accountMenuSource, context, { filename: accountMenuPath });
  return context;
}

function renderAccountMenu({ accounts, login = null, selectedAccountId = null }) {
  const initialState = [
    accounts,
    false,
    "",
    "",
    "",
    login,
    false,
    selectedAccountId,
  ];
  let stateIndex = 0;
  const context = loadAccountMenu({
    BW: () => {},
    CH: { Separator: "separator" },
    Lo: () => ({}),
    Q: {},
    S2: "status-icon",
    _H: "menu-item",
    kXc: {
      useCallback: (callback) => callback,
      useEffect: () => {},
      useState: (fallback) => {
        const supplied = initialState[stateIndex++];
        const value = supplied === undefined
          ? typeof fallback === "function"
            ? fallback()
            : fallback
          : supplied;
        return [value, () => {}];
      },
    },
  });
  return context.CodexMuxAccountMenu();
}

function accountFixture(id, status, overrides = {}) {
  return {
    id,
    label: id,
    enabled: status !== "disabled",
    connected: status === "ready",
    controller: false,
    status,
    rateLimits: null,
    ...overrides,
  };
}

test("usage helpers select the weekly window and clamp remaining capacity", () => {
  const context = loadAccountMenu();
  const rateLimits = {
    primary: {
      usedPercent: 25,
      windowDurationMins: 300,
      resetsAt: 10,
    },
    secondary: {
      usedPercent: 115,
      windowDurationMins: 10_080,
      resetsAt: 20,
    },
  };

  const weekly = context.codexMuxWeeklyWindow(rateLimits);
  assert.equal(weekly.windowDurationMins, 10_080);
  assert.equal(weekly.usedPercent, 115);

  const windows = context.codexMuxUsageWindows(rateLimits);
  assert.equal(windows.length, 2);
  assert.deepEqual(
    Array.from(windows, (entry) => ({
      usedPercent: entry.usedPercent,
      remainingPercent: entry.remainingPercent,
      windowMinutes: entry.windowMinutes,
      resetsAt: entry.resetsAt,
    })),
    [
      {
        usedPercent: 25,
        remainingPercent: 75,
        windowMinutes: 300,
        resetsAt: 10,
      },
      {
        usedPercent: 115,
        remainingPercent: 0,
        windowMinutes: 10_080,
        resetsAt: 20,
      },
    ],
  );
  assert.equal(context.codexMuxWeeklyWindow(null), null);
  assert.deepEqual(Array.from(context.codexMuxUsageWindows(null)), []);
});

test("rate-limit reset control calls are authenticated and account scoped", async () => {
  const requests = [];
  const context = loadAccountMenu({
    fetch: async (url, options) => {
      requests.push({ url, options });
      return {
        ok: true,
        status: 200,
        json: async () => ({ available_count: 2 }),
      };
    },
  });

  await context.codexMuxRateLimitResets("work/team");
  await context.codexMuxConsumeRateLimitReset("work/team", {
    creditId: null,
    redeemRequestId: "redeem-7",
  });

  assert.equal(requests.length, 2);
  assert.match(requests[0].url, /\/accounts\/work%2Fteam\/rate-limit-resets$/);
  assert.equal(requests[0].options.headers["X-Codex-Mux-Token"].length > 0, true);
  assert.match(
    requests[1].url,
    /\/accounts\/work%2Fteam\/rate-limit-resets\/consume$/,
  );
  assert.equal(requests[1].options.method, "POST");
  assert.deepEqual(JSON.parse(requests[1].options.body), {
    creditId: null,
    redeemRequestId: "redeem-7",
  });
});

test("usage subscription selector exposes a labelled group and pressed state", () => {
  const context = loadAccountMenu();
  const accounts = [
    {
      id: "primary",
      label: "Personal",
      planLabel: "Plus",
      profileImageUrl: null,
    },
    {
      id: "second",
      label: "Work",
      planLabel: "Team",
      profileImageUrl: null,
    },
  ];

  const view = context.CodexMuxResetAccountSelector({
    accounts,
    loading: false,
    onSelect: () => {},
    resetCounts: { primary: 1, second: 0 },
    selectedId: "primary",
  });
  const selector = view.props.children[1];
  const buttons = selector.props.children;

  assert.equal(selector.props.role, "group");
  assert.equal(typeof selector.props["aria-label"], "string");
  assert.notEqual(selector.props["aria-label"].trim(), "");
  assert.equal(buttons.length, 2);
  assert.equal(buttons[0].type, "button");
  assert.equal(buttons[0].props.type, "button");
  assert.equal(buttons[0].props["aria-pressed"], true);
  assert.equal(buttons[1].props["aria-pressed"], false);
});

test("account management source keeps status and destructive controls accessible", () => {
  assert.match(accountMenuSource, /data-codex-mux-state/);
  assert.match(accountMenuSource, /data-codex-mux-action/);
  assert.match(accountMenuSource, /aria-label/);
  assert.match(accountMenuSource, /aria-live/);
  assert.match(accountMenuSource, /role:\s*["']alert["']/);
  assert.match(accountMenuSource, /aria-busy/);
  assert.match(accountMenuSource, /Remove subscription/);
});

test("account menu renders pending, error, disabled and disconnected states", () => {
  const accounts = [
    accountFixture("ready", "ready"),
    accountFixture("pending", "pending"),
    accountFixture("error", "error", { error: "network unavailable" }),
    accountFixture("disabled", "disabled"),
    accountFixture("disconnected", "disconnected"),
    accountFixture("restarting", "restarting"),
  ];
  const view = renderAccountMenu({ accounts });
  const rows = view.props.children;
  const states = Object.fromEntries(
    rows
      .filter((row) => row?.props?.["data-codex-mux-account-id"])
      .map((row) => [
        row.props["data-codex-mux-account-id"],
        row.props["data-codex-mux-state"],
      ]),
  );

  assert.deepEqual(states, {
    ready: "ready",
    pending: "pending",
    error: "error",
    disabled: "disabled",
    disconnected: "disconnected",
    restarting: "restarting",
  });
});

test("account menu exposes only actions valid for each lifecycle state", () => {
  const scenarios = [
    ["ready", ["rename", "logout", "disable", "delete"]],
    ["pending", ["rename", "cancel-login", "continue-login"]],
    ["error", ["rename", "login", "disable", "delete"]],
    ["disabled", ["rename", "enable", "delete"]],
    ["disconnected", ["rename", "login", "disable", "delete"]],
    ["restarting", ["rename"]],
  ];

  for (const [status, expectedActions] of scenarios) {
    const account = accountFixture("secondary", status, {
      error: status === "error" ? "backend unavailable" : "",
    });
    const login = status === "pending"
      ? { accountId: account.id, userCode: "ABCD-EFGH" }
      : null;
    const view = renderAccountMenu({
      accounts: [account],
      login,
      selectedAccountId: account.id,
    });
    const actions = Array.from(
      view.props.children
        .map((row) => row?.props?.["data-codex-mux-action"])
        .filter(Boolean),
    );
    assert.deepEqual(actions, [...expectedActions, "add"], status);
  }
});

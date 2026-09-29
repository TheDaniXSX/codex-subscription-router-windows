const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "turn-usage.js"), "utf8");
const tick = () => new Promise(setImmediate);

function makeReact() {
  const values = [];
  const effectDeps = [];
  const effectCleanups = [];
  let cursor = 0;
  let pendingEffects = [];
  return {
    createElement(type, props, ...children) {
      const next = { ...(props || {}) };
      if (children.length === 1) next.children = children[0];
      else if (children.length > 1) next.children = children;
      return { type, props: next };
    },
    begin() {
      cursor = 0;
      pendingEffects = [];
    },
    useState(initialValue) {
      const slot = cursor++;
      if (!(slot in values)) values[slot] = typeof initialValue === "function" ? initialValue() : initialValue;
      return [values[slot], (update) => {
        values[slot] = typeof update === "function" ? update(values[slot]) : update;
      }];
    },
    useRef(initialValue) {
      const slot = cursor++;
      if (!(slot in values)) values[slot] = { current: initialValue };
      return values[slot];
    },
    useEffect(effect, dependencies) {
      const slot = cursor++;
      const previous = effectDeps[slot];
      if (!previous || previous.length !== dependencies.length || dependencies.some((value, index) => value !== previous[index])) {
        effectDeps[slot] = dependencies.slice();
        pendingEffects.push({ slot, effect });
      }
    },
    flushEffects() {
      for (const { slot, effect } of pendingEffects) {
        effectCleanups[slot]?.();
        effectCleanups[slot] = effect();
      }
      pendingEffects = [];
    },
    cleanup() {
      for (const stop of effectCleanups) stop?.();
    },
  };
}

function viewFixture(overrides = {}) {
  return {
    rootTurnId: "root-1",
    threadId: "thread one",
    turnId: "turn/1",
    revision: 7,
    calibrationEnabled: false,
    requests: [{
      requestId: "request-1",
      accountId: "plus-account",
      accountPlan: "Plus",
      modelRequested: "gpt-6-astra",
      modelServed: "gpt-6-astra",
      tierRequested: "standard",
      outcome: "completed",
      subagent: false,
      startedAt: "2026-09-28T12:00:00Z",
      finishedAt: "2026-09-28T12:00:02Z",
      usage: { inputTokens: 100, outputTokens: 50, cachedInputTokens: 20 },
      apiCost: { usd: 0.00125, inputUsd: 0.0004, cachedInputUsd: 0.0001, outputUsd: 0.00075, rateVersion: "2026-09-28" },
      estimatedShort: { percentX20: 0.3, modelVersion: "est-1", confidence: "low" },
      estimatedWeekly: { percentX20: 0.08, modelVersion: "est-1", confidence: "low" },
    }],
    totalUsage: { inputTokens: 100, outputTokens: 50, cachedInputTokens: 20 },
    totalUsageComplete: true,
    associationComplete: true,
    pendingRelations: 0,
    activeDescendants: 0,
    unit: "Pro20",
    totalApiCostUsd: 0.00125,
    estimatedShort: { percentX20: 0.3, modelVersion: "est-1", confidence: "low" },
    estimatedWeekly: { percentX20: 0.08, modelVersion: "est-1", confidence: "low" },
    agents: [],
    observations: [
      {
        id: "obs-short",
        accountId: "plus-account",
        plan: "Plus",
        bucket: "short",
        capacityMultiplier: 1,
        before: { usedPercent: 21, observedAt: "2026-09-28T11:59:59Z", windowDurationMins: 300, source: "rate-limit" },
        after: { usedPercent: 21.4, observedAt: "2026-09-28T12:00:03Z", windowDurationMins: 300, source: "rate-limit" },
        rawDeltaPp: 0.4,
        observedPercentX20: 0.02,
        requestIds: ["request-1"],
        rootTurnIds: ["root-1"],
        calibrationEnabled: false,
        eligible: true,
        revision: 7,
      },
      {
        id: "obs-weekly",
        accountId: "plus-account",
        plan: "Plus",
        bucket: "weekly",
        capacityMultiplier: 1,
        before: { usedPercent: 51, observedAt: "2026-09-28T11:59:59Z", windowDurationMins: 10080 },
        after: { usedPercent: 51.1, observedAt: "2026-09-28T12:00:03Z", windowDurationMins: 10080 },
        rawDeltaPp: 0.1,
        observedPercentX20: 0.005,
        requestIds: ["request-1"],
        rootTurnIds: ["root-1"],
        calibrationEnabled: false,
        eligible: true,
        revision: 7,
      },
    ],
    ...overrides,
  };
}

function fixture() {
  const React = makeReact();
  const requests = [];
  const timers = new Map();
  let timerId = 0;
  const context = vm.createContext({
    AbortController,
    Date,
    encodeURIComponent,
    fetch: (url, options = {}) => new Promise((resolve, reject) => requests.push({ url: String(url), options, resolve, reject })),
    Intl,
    Number,
    React,
    String,
    setTimeout: (callback, delay) => { const id = ++timerId; timers.set(id, { callback, delay }); return id; },
    clearTimeout: (id) => timers.delete(id),
  });
  vm.runInContext(source, context, { filename: path.join(__dirname, "turn-usage.js") });
  let threadId = "thread one";
  let turnId = "turn/1";
  return {
    requests,
    context,
    timers,
    async runTimers() {
      const pending = [...timers.values()];
      timers.clear();
      for (const timer of pending) timer.callback();
      await tick();
    },
    render(nextThreadId = threadId, nextTurnId = turnId) {
      threadId = nextThreadId;
      turnId = nextTurnId;
      React.begin();
      const tree = context.CodexMuxTurnUsage({ threadId, turnId });
      React.flushEffects();
      return tree;
    },
    async respond(index, body, status = 200) {
      requests[index].resolve({ ok: status >= 200 && status < 300, status, json: async () => body });
      await tick();
    },
    async fail(index) {
      requests[index].reject(new Error("offline"));
      await tick();
    },
    close() { React.cleanup(); },
  };
}

function walk(node, predicate, result = []) {
  if (node == null) return result;
  if (Array.isArray(node)) {
    for (const child of node) walk(child, predicate, result);
    return result;
  }
  if (typeof node !== "object") return result;
  if (predicate(node)) result.push(node);
  walk(node.props?.children, predicate, result);
  return result;
}

function textOf(node) {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textOf).join(" ");
  return textOf(node.props?.children);
}

function action(tree, name) {
  return walk(tree, (node) => node.props?.["data-codex-mux-usage-action"] === name)[0];
}

test("turn footer requests the exact turn and displays separate Pro20 observed, estimated and API values", async () => {
  const f = fixture();
  let tree = f.render();
  assert.equal(f.requests.length, 1);
  assert.match(f.requests[0].url, /\/turn\?threadId=thread%20one&turnId=turn%2F1$/);
  assert.equal(f.requests[0].options.headers["X-Codex-Mux-Token"], "__CODEX_MUX_CONTROL_TOKEN__");
  await f.respond(0, viewFixture());
  tree = f.render();
  assert.match(textOf(action(tree, "observed")), /Obs 0\.02% \/ 0\.005%/);
  assert.match(textOf(action(tree, "estimated")), /Est 0\.3% \/ 0\.08%/);
  assert.match(textOf(action(tree, "api-cost")), /API \$0\.0013/);
  assert.equal(action(tree, "calibration").props["aria-pressed"], false);
  assert.match(action(tree, "calibration").props["aria-label"], /consumo externo desconocido/);
  f.close();
});

test("calibration opt-in sends the root id, revision and boolean, then reflects saved state", async () => {
  const f = fixture();
  f.render();
  await f.respond(0, viewFixture());
  let tree = f.render();
  action(tree, "calibration").props.onClick();
  assert.equal(f.requests.length, 2);
  assert.equal(f.requests[1].options.method, "PATCH");
  assert.deepEqual(JSON.parse(f.requests[1].options.body), {
    rootId: "root-1",
    included: true,
    revision: 7,
  });
  await f.respond(1, viewFixture({ calibrationEnabled: true, revision: 8 }));
  tree = f.render();
  assert.equal(action(tree, "calibration").props["aria-pressed"], true);
  assert.match(textOf(tree), /Selección guardada/);
  f.close();
});

test("missing observed, estimated and API data remain unavailable rather than becoming zero", async () => {
  const f = fixture();
  f.render();
  await f.respond(0, viewFixture({
    totalApiCostUsd: undefined,
    estimatedShort: { unavailableReason: "no quota model" },
    estimatedWeekly: { unavailableReason: "no quota model" },
    observations: [],
  }));
  const tree = f.render();
  assert.match(textOf(action(tree, "observed")), /Obs — \/ —/);
  assert.match(textOf(action(tree, "estimated")), /Est — \/ —/);
  assert.match(textOf(action(tree, "api-cost")), /API —/);
  assert.doesNotMatch(textOf(tree), /0%/);
  assert.equal(f.render(null, null), null);
  f.close();
});

test("observations in multiple groups are counted as groups, never summed onto a request", async () => {
  const base = viewFixture();
  base.observations.push({ ...base.observations[0], id: "obs-short-2", observedPercentX20: 0.04 });
  const f = fixture();
  f.render();
  await f.respond(0, base);
  const tree = f.render();
  assert.match(textOf(action(tree, "observed")), /Obs 0\.06% \/ 0\.005%/);
  f.close();
});

test("expanded detail exposes shared group selection and marks all known roots atomically", async () => {
  const view = viewFixture();
  view.observations[0].rootTurnIds = ["root-1", "root-2", "root-3"];
  view.observations[0].calibrationEnabled = false;
  const f = fixture();
  f.render();
  await f.respond(0, view);
  let tree = f.render();
  await action(tree, "observed").props.onClick();
  assert.match(f.requests[1].url, /\/turn\?/);
  assert.match(f.requests[2].url, /\/status$/);
  await f.respond(1, view);
  await f.respond(2, { buckets: [{ bucket: "short", sampleCount: 3, confidence: "medium" }] });
  tree = f.render();
  assert.match(textOf(tree), /Ventana corta: 0\.02%/);
  assert.match(textOf(tree), /3 muestras/);
  const includeGroup = action(tree, "include-group");
  assert.ok(includeGroup);
  includeGroup.props.onClick();
  assert.equal(f.requests[3].options.method, "PATCH");
  assert.deepEqual(JSON.parse(f.requests[3].options.body), {
    rootId: "root-1",
    included: true,
    revision: 7,
    includeRelated: true,
  });
  f.close();
});

test("a stale turn response is not rendered after navigating to another turn", async () => {
  const f = fixture();
  f.render();
  f.render("other-thread", "other-turn");
  await f.respond(0, viewFixture());
  const tree = f.render("other-thread", "other-turn");
  assert.match(f.requests[1].url, /threadId=other-thread&turnId=other-turn/);
  assert.doesNotMatch(textOf(tree), /root-1|Plus|gpt-6-astra/);
  await f.respond(1, viewFixture({ rootTurnId: "root-2", threadId: "other-thread", turnId: "other-turn" }));
  assert.match(textOf(f.render("other-thread", "other-turn")), /API \$0\.0013/);
  f.close();
});

test("shared groups sum once per id while unknown capacity and missing readings remain unavailable", async () => {
  const view = viewFixture();
  view.observations[0].rootTurnIds.push("other-root");
  view.observations.push({ ...view.observations[0] });
  view.observations.push({ ...view.observations[0], id: "unknown-capacity", capacityMultiplier: 0, observedPercentX20: undefined });
  const f = fixture();
  f.render();
  await f.respond(0, view);
  const tree = f.render();
  assert.equal(textOf(action(tree, "observed")).trim(), "Obs 0.02% parcial* / 0.005%");
  assert.equal(f.context.codexMuxUsageObservedValue({ rawDeltaPp: 1, capacityMultiplier: 0 }), null);
  assert.equal(f.context.codexMuxUsageFormatPercent(0.000000025), "2.5e-8%");
  assert.equal(f.context.codexMuxUsageFormatPercent(0), "0%");
  assert.equal(f.context.codexMuxUsageFormatUsd(0.0000005), "$5e-7");
  f.close();
});

test("partial known API cost is a lower bound, not a complete total", async () => {
  const f = fixture();
  f.render();
  await f.respond(0, viewFixture({ totalApiCostUsd: undefined, totalKnownApiCostUsd: 0.00125 }));
  const tree = f.render();
  assert.equal(textOf(action(tree, "api-cost")).trim(), "API ≥$0.0013 parcial");
  assert.match(action(tree, "api-cost").props.title, /faltan categorías/);
  f.close();
});

test("initial polling covers the 20 second quota settling period and is bounded after 30 seconds", async () => {
  const f = fixture();
  f.render();
  for (let index = 0; index < 16; index++) {
    assert.equal(f.requests.length, index + 1);
    await f.respond(index, viewFixture({ revision: index, activeDescendants: index < 15 ? 1 : 0 }));
    f.render();
    if (index < 15) {
      assert.equal([...f.timers.values()][0].delay, 2000);
      await f.runTimers();
    }
  }
  assert.equal(f.timers.size, 0);
  await f.runTimers();
  assert.equal(f.requests.length, 16);
  f.close();
});

test("expanded detail refreshes after initial polling stops and closing cancels its timer", async () => {
  const f = fixture();
  f.render();
  for (let index = 0; index < 16; index++) {
    await f.respond(index, viewFixture({ revision: index }));
    f.render();
    if (index < 15) await f.runTimers();
  }
  action(f.render(), "estimated").props.onClick();
  f.render();
  await f.respond(16, viewFixture({ revision: 16 }));
  await f.respond(17, { buckets: [] });
  await f.runTimers();
  assert.equal(f.requests.length, 19);
  await f.respond(18, viewFixture({ revision: 17, totalApiCostUsd: 0.25 }));
  const tree = f.render();
  assert.equal(textOf(action(tree, "api-cost")).trim(), "API $0.250");
  action(tree, "estimated").props.onClick();
  f.render();
  assert.equal(f.timers.size, 0);
  f.close();
});

test("navigation aborts every pending request and a late calibration response cannot overwrite the next turn", async () => {
  const f = fixture();
  f.render();
  await f.respond(0, viewFixture());
  action(f.render(), "calibration").props.onClick();
  f.render("new-thread", "new-turn");
  assert.equal(f.requests[1].options.signal.aborted, true);
  await f.respond(2, viewFixture({ rootTurnId: "root-new", threadId: "new-thread", turnId: "new-turn", totalApiCostUsd: 9 }));
  await f.respond(1, viewFixture({ calibrationEnabled: true, revision: 100 }));
  const tree = f.render();
  assert.equal(textOf(action(tree, "api-cost")).trim(), "API $9.000");
  assert.equal(action(tree, "calibration").props["aria-pressed"], false);
  assert.doesNotMatch(textOf(tree), /Selección guardada/);
  f.close();
});

test("revision conflicts reload current state and stale background revisions do not roll back selection", async () => {
  const f = fixture();
  f.render();
  await f.respond(0, viewFixture());
  action(f.render(), "calibration").props.onClick();
  await f.respond(1, { error: "revision conflict" }, 409);
  assert.match(f.requests[2].url, /\/turn\?/);
  await f.respond(2, viewFixture({ revision: 9, calibrationEnabled: true }));
  assert.equal(action(f.render(), "calibration").props["aria-pressed"], true);
  await f.runTimers();
  await f.respond(3, viewFixture({ revision: 8, calibrationEnabled: false }));
  const tree = f.render();
  assert.equal(action(tree, "calibration").props["aria-pressed"], true);
  assert.match(textOf(tree), /estado actual/);
  f.close();
});

test("details preserve per agent ancestry, pricing categories, snapshot samples and estimator metadata", async () => {
  const view = viewFixture();
  const root = view.requests[0];
  root.threadId = view.threadId;
  root.turnId = view.turnId;
  root.usage = { ...root.usage, cacheWriteTokens: 5, reasoningTokens: 10, source: "response.completed", quality: "reported" };
  root.apiCost.rateCard = { apiTier: "standard", inputUsdPerMillion: 10, cachedInputUsdPerMillion: 1, cacheWriteUsdPerMillion: 12.5, outputUsdPerMillion: 50 };
  root.apiCost.cacheWriteUsd = 0.0000625;
  root.estimatedShort = { percentX20: 0.02, modelVersion: "fit-v1", sampleCount: 12, confidence: "medium", coefficient: 0.5, fastMultiplier: 2.5, planMultiplier: 1 };
  view.requests.push({ ...root, requestId: "continuation-1", parentRequestId: "request-1" });
  view.requests.push({ ...root, requestId: "child-1", agentId: "agent-1", subagent: true, threadId: "child-thread", parentThreadId: view.threadId });
  view.observations[0].samples = [{ usedPercent: 21.2, observedAt: "2026-09-28T12:00:01Z", source: "poll-2s", quality: "settling" }];
  const f = fixture();
  f.render();
  await f.respond(0, view);
  action(f.render(), "observed").props.onClick();
  const tree = f.render();
  const rootNode = walk(tree, (node) => node.props?.["data-codex-mux-usage-request"] === "request-1")[0];
  assert.equal(walk(rootNode, (node) => node.props?.["data-codex-mux-usage-request"] === "continuation-1").length, 1);
  assert.equal(walk(tree, (node) => node.type === "details" && node.props?.["data-codex-mux-usage-agent"] === "agent-1").length, 1);
  assert.match(textOf(tree), /caché escrita 5/);
  assert.match(textOf(tree), /contexto corto.*entrada 10.*caché leída 1.*caché escrita 12\.5.*salida 50/);
  assert.match(textOf(tree), /poll-2s.*settling/);
  assert.match(textOf(tree), /Normalización Pro ×20: 0\.4 pp × 1 \/ 20 = 0\.02%/);
  assert.match(textOf(tree), /12 muestras.*fit-v1.*coeficiente 0\.5 pp Pro ×20 \/ USD relativo.*factor Fast supuesto 2\.5/);
  assert.match(textOf(tree), /Chat child-thread.*chat padre thread one/);
  assert.equal(action(tree, "observed").props["aria-expanded"], true);
  assert.equal(walk(tree, (node) => node.props?.id === action(tree, "observed").props["aria-controls"]).length, 1);
  assert.equal(walk(tree, (node) => node.type === "button" && walk(node.props.children, (child) => child.type === "button").length > 0).length, 0);
  f.close();
});

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

function harness({ fetch = async () => { throw Error("Unexpected request"); } } = {}) {
  const accounts = [
    { id: "primary", label: "Primary", enabled: true, connected: true, controller: true },
    { id: "second", label: "Second", enabled: true, connected: false },
    { id: "disabled", label: "Disabled", enabled: false },
  ];
  const values = [accounts, false, "", "", "", null, false, null];
  let index = 0;
  let focused = 0;
  const effects = [];
  const jsx = (type, props, key) => ({ type, props, key });
  const context = vm.createContext({
    AbortController, TextDecoder, TextEncoder, fetch, setTimeout, clearTimeout,
    setInterval, clearInterval,
    document: { querySelector: () => ({ focus: () => focused++ }) },
    e7: { jsx, jsxs: jsx, Fragment: "fragment" },
    BW: () => {}, Lo: () => ({}), Q: {}, S2: "icon", _H: "menu-item",
    CH: { Separator: "separator" },
    kXc: {
      useState: (initial) => {
        const slot = index++;
        if (!(slot in values)) values[slot] = typeof initial === "function" ? initial() : initial;
        return [values[slot], (next) => { values[slot] = typeof next === "function" ? next(values[slot]) : next; }];
      },
      useCallback: (callback) => callback,
      useEffect: (effect) => { effects.push(effect); },
    },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "account-menu.js"), "utf8"), context);
  const render = () => { index = 0; return context.CodexMuxAccountMenu().props.children; };
  const trigger = (rows) => rows.find((row) => row.props["data-codex-mux-action"] === "routing-mode");
  const options = (rows) => rows.find((row) => row.props.id === "codex-mux-routing-options").props.children;
  return { render, trigger, options, values, effects, context, focused: () => focused };
}

const event = { preventDefault() {} };
const settle = () => new Promise((resolve) => setImmediate(resolve));

test("selector starts collapsed and exposes Auto plus every subscription without managing it", () => {
  const h = harness();
  const rows = h.render();
  assert.equal(rows[0].props.children, "Usage remaining");
  assert.equal(rows[1].props.children, "Routing mode");
  assert.equal(h.trigger(rows).props["aria-expanded"], false);
  h.trigger(rows).props.onSelect(event);
  const options = h.options(h.render());
  assert.equal(options.length, 4);
  assert.equal(options[0].props.role, "menuitemradio");
  assert.equal(options[0].props["aria-checked"], true);
  assert.equal(options[1].props.disabled, false, "Primary must be selectable");
  assert.match(options[2].props.SubText, /Disconnected/);
  assert.equal(options[3].props.disabled, true);
  assert.equal(h.values[7], null, "account management remains closed");
});

test("selection persists only after authenticated PATCH succeeds and returns focus", async () => {
  const requests = [];
  let finish;
  const h = harness({ fetch: async (url, options) => {
    requests.push({ url, options });
    await new Promise((resolve) => { finish = resolve; });
    return { ok: true, json: async () => ({ routingMode: { mode: "account", accountId: "primary" } }) };
  } });
  h.trigger(h.render()).props.onSelect(event);
  h.options(h.render())[1].props.onSelect(event);
  assert.equal(h.values[8].mode, "auto");
  assert.equal(h.trigger(h.render()).props["aria-busy"], true);
  assert.equal(requests[0].options.method, "PATCH");
  assert.deepEqual(JSON.parse(requests[0].options.body), { mode: "account", accountId: "primary" });
  assert.ok(requests[0].options.headers["X-Codex-Mux-Token"]);
  finish();
  await settle();
  assert.equal(h.values[8].accountId, "primary");
  assert.equal(h.trigger(h.render()).props["aria-expanded"], false);
  assert.match(h.trigger(h.render()).props["aria-label"], /Primary/);
  assert.equal(h.focused(), 1);
  assert.equal(requests.length, 1);
});

test("failed selection retains mode and choices, reports error and releases busy state", async () => {
  const h = harness({ fetch: async () => { throw Error("Router unavailable"); } });
  h.trigger(h.render()).props.onSelect(event);
  h.options(h.render())[1].props.onSelect(event);
  await settle();
  assert.equal(h.values[8].mode, "auto");
  assert.equal(h.values[3], "Router unavailable");
  assert.equal(h.trigger(h.render()).props["aria-expanded"], true);
  assert.equal(h.trigger(h.render()).props["aria-busy"], false);
});

test("Escape collapses the bounded option list and restores selector focus", () => {
  const h = harness();
  h.trigger(h.render()).props.onSelect(event);
  const group = h.render().find((row) => row.props.id === "codex-mux-routing-options");
  assert.equal(group.props.style.overflowY, "auto");
  let stopped = false;
  group.props.onKeyDown({ key: "Escape", preventDefault() {}, stopPropagation() { stopped = true; } });
  assert.equal(stopped, true);
  assert.equal(h.trigger(h.render()).props["aria-expanded"], false);
  assert.equal(h.focused(), 1);
});

test("routing event during save reconciles a concurrent client change after the PATCH", async () => {
  let finishPatch;
  let onMessage;
  let reads = 0;
  const h = harness({ fetch: async (url, options) => {
    if (options.method === "PATCH") {
      await new Promise((resolve) => { finishPatch = resolve; });
      return { ok: true, json: async () => ({ routingMode: { mode: "account", accountId: "primary" } }) };
    }
    reads++;
    return { ok: true, json: async () => ({ accounts: h.values[0], routingMode: { mode: "auto" } }) };
  } });
  h.context.codexMuxSubscribeEvents = (options) => { onMessage = options.onMessage; return () => {}; };
  h.context.setTimeout = () => 0;
  h.context.setInterval = () => 0;
  h.render();
  const cleanup = h.effects[0]();
  await settle();
  h.trigger(h.render()).props.onSelect(event);
  h.options(h.render())[1].props.onSelect(event);
  onMessage({ data: JSON.stringify({ type: "routing-mode-updated" }) });
  assert.equal(reads, 1, "reconciliation waits until the pending write finishes");
  finishPatch();
  await settle();
  assert.equal(reads, 2);
  assert.equal(h.values[8].mode, "auto", "latest authoritative selection wins");
  cleanup();
});

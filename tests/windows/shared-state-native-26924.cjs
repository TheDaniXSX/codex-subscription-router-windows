// Runs reviewed native bundle excerpts against synthetic state only.
// Usage: node tests/windows/shared-state-native-26924.cjs <patched-extraction>
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const test = require("node:test");

const root = path.resolve(process.argv[2]);
const build = path.join(root, ".vite/build");
const assets = path.join(root, "webview/assets");
const load = (directory, prefix) => {
  const files = fs.readdirSync(directory).filter(name => name.startsWith(prefix) && name.endsWith(".js"));
  assert.equal(files.length, 1, prefix);
  const file = path.join(directory, files[0]);
  return { file, source: fs.readFileSync(file, "utf8") };
};
const policy = load(build, "policy-");
const main = load(build, "main-");
const initial = load(assets, "app-initial-");
const shared = require(path.join(build, "codex-router-shared-global-state.cjs"));
const between = (source, start, end) => {
  const first = source.indexOf(start);
  assert.notEqual(first, -1, start);
  const last = source.indexOf(end, first + start.length);
  assert.notEqual(last, -1, end);
  return source.slice(first, last);
};

test("real patched 26.924 store constructs and merges independently instantiated native writers", async t => {
  const nativeClass = between(policy.source, "var wr=class{", ",Tr=new Set");
  const sandbox = {
    require: createRequire(policy.file), Map, Set, Promise, queueMicrotask,
    r: { Lt: () => () => ({ warning() {} }) }, n: { Na: () => null },
    Dr: file => new Map(Object.entries(shared.read(file))),
    d: () => Object.assign(() => {}, { cancel() {} }), fr: 25,
  };
  vm.runInNewContext(nativeClass + ";globalThis.NativeStore=wr", sandbox);
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "csr-native-global-state-"));
  const file = path.join(directory, "state.json");
  fs.writeFileSync(file, JSON.stringify({ records: { a: 1, b: 1 } }));
  const a = new sandbox.NativeStore(file), b = new sandbox.NativeStore(file);
  t.after(async () => {
    await a.__csrSharedGlobalState.close().catch(() => {});
    await b.__csrSharedGlobalState.close().catch(() => {});
    fs.rmSync(directory, { recursive: true, force: true });
  });
  a.update("records", value => ({ ...value, a: 2 }));
  b.update("records", value => ({ ...value, b: 3 }));
  await Promise.all([a.flushOrThrow(), b.flushOrThrow()]);
  assert.deepEqual(shared.read(file).records, { a: 2, b: 3 });
});

test("real renderer atom sender transports immutable baseline and advances its optimistic copy", () => {
  const sender = between(initial.source, "function peo(e,t,n){", "function meo(");
  const messages = [];
  const sandbox = { csrSharedAtomBases: { settings: { a: 1 } }, Kp: { isHostBacked: () => false }, aa: { dispatchMessage: (type, value) => messages.push({ type, value }) } };
  vm.runInNewContext(sender + ";globalThis.send=peo", sandbox);
  const value = { a: 2 };
  sandbox.send("settings", value);
  value.a = 99;
  sandbox.send("settings", { a: 3 });
  assert.equal(messages[0].value.csrBase.value.a, 1);
  assert.equal(messages[1].value.csrBase.value.a, 2);
  assert.equal(messages[0].type, "persisted-atom-update");
  sandbox.send("new", true);
  assert.equal(messages[2].value.csrBase.present, false);
});

test("real native renderer mutation methods route baseline through helper and preserve window ownership", async () => {
  const update = between(main.source, "updatePersistedAtomState(e,t,n,r,s){", "broadcastPersistedAtomUpdate(");
  const calls = [];
  const sandbox = { jZ: key => key === "private-tab", require: () => ({ setRendererAtom: (...args) => { calls.push(args); return Promise.resolve(); } }) , uVe() {} };
  vm.runInNewContext("globalThis.Native=class{" + update + "}", sandbox);
  const handler = new sandbox.Native();
  handler.browserHostManager = { isPrimaryWindowPersistenceOwner: () => false };
  const contents = {}, expected = { present: true, value: 1 };
  await handler.updatePersistedAtomState(contents, "private-tab", 2, null, expected);
  await handler.updatePersistedAtomState(contents, "unread-thread-ids-by-host-v1", 2, null, expected);
  assert.equal(calls.length, 0);
  await handler.updatePersistedAtomState(contents, "shared", 2, null, expected);
  assert.equal(calls.length, 1);
  assert.equal(calls[0][5], expected);
  assert.equal(calls[0][0], handler);
});

test("real get/set global handlers transport snapshots and retain protected-key restrictions", async () => {
  const handlers = between(main.source, '"get-global-state":async(', ',"queued-follow-up-send-lock-acquire"');
  const calls = [];
  const sandbox = { _H: new Set(["protected"]), r: { Ma: { THREAD_PROJECT_ASSIGNMENTS: "assignments", PROJECTLESS_THREAD_IDS: "projectless" } }, require: () => ({ rendererSnapshot: () => ({ present: true, value: { a: 1 } }), setRendererGlobal: (...args) => { calls.push(args); return { success: true }; } }) };
  vm.runInNewContext("globalThis.Native=class{getGlobalStateValue(){return {a:1}}handlers={" + handlers + "}}", sandbox);
  const handler = new sandbox.Native();
  const read = await handler.handlers["get-global-state"]({ key: "example" });
  assert.equal(read.csrBase.value.a, 1);
  const expected = read.csrBase;
  await handler.handlers["set-global-state"]({ key: "example", value: { a: 2 }, csrBase: expected });
  assert.equal(calls[0][3], expected);
  assert.equal((await handler.handlers["set-global-state"]({ key: "assignments" })).success, false);
  await assert.rejects(handler.handlers["set-global-state"]({ key: "protected" }), /not allowed/);
});

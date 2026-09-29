const assert = require("node:assert/strict");
const fs = require("node:fs");
const fsp = fs.promises;
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const test = require("node:test");
const shared = require("./shared-global-state.cjs");

function makeStore(file, options = {}) {
  const store = {
    filePath: file, state: new Map(Object.entries(shared.read(file))), changeListeners: new Map(),
    get(key) { return this.state.get(key) ?? null; },
    getStored(key) { return this.state.get(key); },
    logger: () => ({ warning() {} }),
  };
  shared.install(store, { debounceMs: 60_000, pollMs: 60_000, watch: false, ...options });
  return store;
}
async function fixture(t, initial = {}) {
  const directory = await fsp.mkdtemp(path.join(os.tmpdir(), "csr-global-state-test-"));
  const file = path.join(directory, "global.json");
  await fsp.writeFile(file, JSON.stringify(initial));
  const stores = [];
  t.after(async () => {
    for (const store of stores) await store.__csrSharedGlobalState.close().catch(() => {});
    // This exclusively test-created directory is the only recursive cleanup target.
    await fsp.rm(directory, { recursive: true, force: true });
  });
  return { file, directory, store(options) { const store = makeStore(file, options); stores.push(store); return store; } };
}

test("independent instances merge distinct leaves and preserve unknown keys", async t => {
  const f = await fixture(t, { projects: { one: { name: "One", color: "blue" } }, unknown: { preserve: true } });
  const a = f.store(), b = f.store();
  a.update("projects", projects => ({ ...projects, one: { ...projects.one, name: "Changed" } }));
  b.update("projects", projects => ({ ...projects, one: { ...projects.one, color: "red" } }));
  await Promise.all([a.flushOrThrow(), b.flushOrThrow()]);
  assert.deepEqual(shared.read(f.file), { projects: { one: { name: "Changed", color: "red" } }, unknown: { preserve: true } });
  a.__csrSharedGlobalState.refresh();
  assert.equal(a.get("projects").one.color, "red");
});

test("mutable objects have a cloned baseline and queued values cannot mutate afterward", async t => {
  const f = await fixture(t, { atoms: { a: 1, b: 1 } });
  const a = f.store(), b = f.store();
  const value = a.get("atoms");
  value.a = 2;
  a.set("atoms", value);
  value.a = 99;
  const remote = b.get("atoms");
  remote.b = 3;
  b.set("atoms", remote);
  await b.flushOrThrow();
  await a.flushOrThrow();
  assert.deepEqual(shared.read(f.file).atoms, { a: 2, b: 3 });
});

test("held mutable references cannot overwrite a watched external update", async t => {
  const f = await fixture(t, { atoms: { a: 1, b: 1 } });
  const a = f.store(), b = f.store();
  const held = a.get("atoms");
  b.update("atoms", value => ({ ...value, b: 5 }));
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  held.a = 2;
  a.set("atoms", held);
  await a.flushOrThrow();
  assert.deepEqual(shared.read(f.file).atoms, { a: 2, b: 5 });
});

test("untracked stale clones retain the pre-refresh baseline and cannot restore external leaves", async t => {
  const f = await fixture(t, { atoms: { a: 1, b: 1 } });
  const a = f.store(), b = f.store();
  const held = { ...a.get("atoms") };
  b.update("atoms", value => ({ ...value, b: 5 }));
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  held.a = 2;
  a.set("atoms", held);
  await a.flushOrThrow();
  assert.deepEqual(shared.read(f.file).atoms, { a: 2, b: 5 });
});

test("renderer global mutations carry their original snapshot across an external refresh", async t => {
  const f = await fixture(t, { projects: { a: { name: "A", color: "blue" } } });
  const a = f.store(), b = f.store();
  const handler = { globalState: a, setGlobalStateValue: (key, value) => a.set(key, value) };
  const expected = shared.rendererSnapshot(handler, "projects");
  b.update("projects", value => ({ a: { ...value.a, color: "red" } }));
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  await shared.setRendererGlobal(handler, "projects", { a: { name: "A2", color: "blue" } }, expected);
  assert.deepEqual(shared.read(f.file).projects, { a: { name: "A2", color: "red" } });
  await assert.rejects(shared.setRendererGlobal(handler, "projects", { a: { name: "stale", color: "blue" } }, expected), { code: "CSR_STATE_CONFLICT" });
  assert.deepEqual(shared.read(f.file).projects, { a: { name: "A2", color: "red" } });
});

test("renderer writes without a baseline fail closed and trigger a refresh", async t => {
  const f = await fixture(t, { projects: { a: 1 } });
  const a = f.store(), events = [];
  a.__csrSharedGlobalState.subscribe(event => events.push(event));
  const handler = { globalState: a, setGlobalStateValue: (key, value) => a.set(key, value) };
  await assert.rejects(shared.setRendererGlobal(handler, "projects", {}, undefined), { code: "CSR_STATE_BASE_REQUIRED" });
  assert.deepEqual(shared.read(f.file).projects, { a: 1 });
  assert.ok(events.some(event => event.reason === "rejected" && event.keys.includes("projects")));
  assert.equal(a.__csrSharedGlobalState.pendingCount, 0);
});

test("renderer atom snapshots preserve remote leaves, reject conflicts and support initial creation", async t => {
  const key = "electron-persisted-atom-state";
  const f = await fixture(t, { [key]: { settings: { a: 1, b: 1 }, other: true } });
  const a = f.store(), b = f.store();
  let syncs = 0;
  const broadcasts = [];
  const handler = { globalState: a, sendPersistedAtomState() { syncs++; }, broadcastPersistedAtomUpdate: (_contents, key, value) => broadcasts.push({ key, value }) };
  const expected = { present: true, value: { a: 1, b: 1 } };
  b.update(key, value => ({ ...value, settings: { ...value.settings, b: 5 } }));
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  await shared.setRendererAtom(handler, {}, "settings", { a: 2, b: 1 }, null, expected);
  assert.deepEqual(shared.read(f.file)[key], { settings: { a: 2, b: 5 }, other: true });
  await shared.setRendererAtom(handler, {}, "settings", { a: 3, b: 1 }, null, expected);
  assert.deepEqual(shared.read(f.file)[key].settings, { a: 2, b: 5 });
  assert.equal(syncs, 1);
  assert.deepEqual(broadcasts[0], { key: "settings", value: { a: 2, b: 5 } });
  const empty = await fixture(t), c = empty.store();
  await shared.setRendererAtom({ ...handler, globalState: c }, {}, "initial", true, null, { present: false });
  assert.deepEqual(shared.read(empty.file)[key], { initial: true });
});

test("renderer record deltas merge through their own baseline without replacing fresh siblings", async t => {
  const key = "electron-persisted-atom-state";
  const f = await fixture(t, { [key]: { records: { a: 1, b: 1 } } });
  const a = f.store(), b = f.store();
  b.update(key, value => ({ records: { ...value.records, b: 5 } }));
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  const handler = { globalState: a, sendPersistedAtomState() {}, broadcastPersistedAtomUpdate() {} };
  await shared.setRendererAtom(handler, {}, "records", undefined, { entries: { a: 2 } }, { present: true, value: { a: 1, b: 1 } }, (value, update) => Object.assign(value, update.entries));
  assert.deepEqual(shared.read(f.file)[key].records, { a: 2, b: 5 });
});

test("stale renderer atoms never recreate a container deleted by another process", async t => {
  const key = "electron-persisted-atom-state";
  const f = await fixture(t, { [key]: { setting: true } });
  const a = f.store(), b = f.store();
  b.delete(key);
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  let refreshed = false;
  const handler = { globalState: a, sendPersistedAtomState() { refreshed = true; }, broadcastPersistedAtomUpdate() {} };
  await shared.setRendererAtom(handler, {}, "setting", false, null, { present: true, value: true });
  assert.equal(Object.hasOwn(shared.read(f.file), key), false);
  assert.equal(refreshed, true);
  assert.equal(a.__csrSharedGlobalState.lastError.code, "CSR_STATE_CONFLICT");
});

test("same-leaf conflicts preserve external state, journal rejected intent and report failure", async t => {
  const f = await fixture(t, { atoms: { name: "original" } });
  const a = f.store(), b = f.store(), events = [];
  a.__csrSharedGlobalState.subscribe(event => events.push(event));
  a.update("atoms", value => ({ ...value, name: "local" }));
  b.update("atoms", value => ({ ...value, name: "remote" }));
  await b.flushOrThrow();
  await assert.rejects(a.flushOrThrow(), { code: "CSR_STATE_CONFLICT" });
  assert.equal(shared.read(f.file).atoms.name, "remote");
  assert.equal(a.get("atoms").name, "remote");
  assert.equal(a.__csrSharedGlobalState.conflicts[0].operations[0].after, "local");
  assert.equal((await fsp.readdir(`${f.file}.csr-conflicts`)).length, 1);
  assert.ok(events.some(event => event.error?.code === "CSR_STATE_CONFLICT"));
  await assert.rejects(a.flushOrThrow(), { code: "CSR_STATE_CONFLICT" });
  a.update("atoms", value => ({ ...value, name: "retry" }));
  await a.flushOrThrow();
  assert.equal(shared.read(f.file).atoms.name, "retry");
});

test("arrays are atomic CAS and concurrent reorder never unions or resurrects deletion", async t => {
  const f = await fixture(t, { order: ["a", "b", "c"] });
  const a = f.store(), b = f.store();
  a.set("order", ["b", "a", "c"]);
  b.set("order", ["a", "c"]);
  await b.flushOrThrow();
  await assert.rejects(a.flushOrThrow(), { code: "CSR_STATE_CONFLICT" });
  assert.deepEqual(shared.read(f.file).order, ["a", "c"]);
});

test("delete against modified object conflicts without restoring or erasing external values", async t => {
  const f = await fixture(t, { projects: { one: { name: "One" } } });
  const a = f.store(), b = f.store();
  a.set("projects", {});
  b.update("projects", value => ({ ...value, one: { name: "Modified" } }));
  await b.flushOrThrow();
  await assert.rejects(a.flushOrThrow(), { code: "CSR_STATE_CONFLICT" });
  assert.deepEqual(shared.read(f.file).projects, { one: { name: "Modified" } });
});

test("identical values and refreshes do not write unchanged state", async t => {
  const f = await fixture(t, { order: ["a", "b"] });
  const a = f.store();
  const before = fs.statSync(f.file).mtimeMs;
  a.set("order", ["a", "b"]);
  await a.flushOrThrow();
  assert.equal(a.__csrSharedGlobalState.refresh(), false);
  assert.equal(fs.statSync(f.file).mtimeMs, before);
  assert.equal(fs.existsSync(`${f.file}.bak`), false);
});

test("transaction callback reads the latest persisted value under the lock", async t => {
  const f = await fixture(t, { counter: 0 });
  const a = f.store(), b = f.store();
  await Promise.all([a.updateAndPersist("counter", n => n + 1), b.updateAndPersist("counter", n => n + 1)]);
  assert.equal(shared.read(f.file).counter, 2);
});

test("corrupt primary file is never overwritten with an empty or stale map", async t => {
  const f = await fixture(t, { a: 1 });
  const a = f.store();
  a.set("a", 2);
  await fsp.writeFile(f.file, "{broken");
  await assert.rejects(a.flushOrThrow());
  assert.equal(fs.readFileSync(f.file, "utf8"), "{broken");
  assert.equal(a.__csrSharedGlobalState.pendingCount, 1);
  await fsp.writeFile(f.file, JSON.stringify({ a: 1 }));
  await a.flushOrThrow();
  assert.equal(shared.read(f.file).a, 2);
});

test("locks have deadlines, preserve live/reused PIDs and recover proven-dead owners", async t => {
  const f = await fixture(t);
  const lock = `${f.file}.csr-lock`;
  await fsp.mkdir(lock);
  await fsp.writeFile(path.join(lock, "owner.json"), JSON.stringify({ pid: process.pid, nonce: "existing", startedAt: 0 }));
  await assert.rejects(shared.acquire(f.file, { timeoutMs: 20, retryMs: 5 }), { code: "CSR_STATE_BUSY" });
  assert.equal(JSON.parse(fs.readFileSync(path.join(lock, "owner.json"))).nonce, "existing");
  const release = await shared.acquire(f.file, { timeoutMs: 100, retryMs: 5, isAlive: pid => pid !== process.pid });
  assert.notEqual(JSON.parse(fs.readFileSync(path.join(lock, "owner.json"))).nonce, "existing");
  await release();
  assert.equal(fs.existsSync(lock), false);
});

test("native notification bridge refreshes atoms, projects and emits one visible conflict per event", async t => {
  const f = await fixture(t, { "electron-persisted-atom-state": {}, "local-projects": {} });
  const a = f.store(), b = f.store(), messages = [], warnings = [];
  let synced = 0, refreshed = 0;
  const backend = { projectsReady: true, ensureProjectsReady: async () => { refreshed++; } };
  const handler = { globalState: a, projectsManager: { projectBackend: backend }, sendMessageToView: (_contents, message) => messages.push(message), sendPersistedAtomState: () => { synced++; } };
  shared.bindRenderer(handler, { once() {}, isDestroyed: () => false }, text => warnings.push(text));
  shared.bindRenderer(handler, { once() {}, isDestroyed: () => false }, text => warnings.push(text));
  b.set("local-projects", { one: { name: "One" } });
  b.set("electron-persisted-atom-state", { selected: true });
  await b.flushOrThrow();
  a.__csrSharedGlobalState.refresh();
  await new Promise(setImmediate);
  assert.equal(synced, 2);
  assert.equal(refreshed, 1);
  assert.ok(messages.some(message => message.type === "workspace-root-options-updated"));
  a.set("local-projects", { one: { name: "Local" } });
  b.set("local-projects", { one: { name: "Remote" } });
  await b.flushOrThrow();
  await assert.rejects(a.flushOrThrow(), { code: "CSR_STATE_CONFLICT" });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /versión compartida/);
});

test("separate Node processes commit from a shared initial snapshot without losing leaves", async t => {
  const f = await fixture(t, { atoms: { a: 0, b: 0 } });
  const helperPath = require.resolve("./shared-global-state.cjs");
  const script = `const shared=require(process.argv[1]);const file=process.argv[2],key=process.argv[3];const store={filePath:file,state:new Map(Object.entries(shared.read(file))),changeListeners:new Map(),get(k){return this.state.get(k)},getStored(k){return this.state.get(k)}};shared.install(store,{watch:false,pollMs:60000,debounceMs:60000});process.send('ready');process.on('message',async()=>{try{store.update('atoms',v=>({...v,[key]:1}));await store.flushOrThrow();await store.__csrSharedGlobalState.close();process.send('done');process.disconnect()}catch(e){process.send({error:e.message});process.exitCode=1;process.disconnect()}});`;
  const children = ["a", "b"].map(key => spawn(process.execPath, ["-e", script, helperPath, f.file, key], { stdio: ["ignore", "pipe", "pipe", "ipc"], windowsHide: true }));
  t.after(() => children.forEach(child => { if (child.exitCode === null) child.kill(); }));
  const receive = child => new Promise((resolve, reject) => { child.once("message", message => typeof message === "object" ? reject(new Error(message.error)) : resolve(message)); child.once("error", reject); });
  await Promise.all(children.map(receive));
  const completed = children.map(receive);
  children.forEach(child => child.send("go"));
  assert.deepEqual(await Promise.all(completed), ["done", "done"]);
  assert.deepEqual(shared.read(f.file).atoms, { a: 1, b: 1 });
});

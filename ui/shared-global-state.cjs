"use strict";

// Coordination for the exact reviewed desktop global-state adapter. Never use
// this as a generic JSON last-writer-wins store: arrays are atomic CAS values.
const fs = require("node:fs");
const fsp = fs.promises;
const path = require("node:path");
const crypto = require("node:crypto");
const { isDeepStrictEqual } = require("node:util");

const MISSING = Symbol("missing");
const ATOMS = "electron-persisted-atom-state";
const PROJECT_KEYS = new Set([
  "local-projects", "project-order", "project-appearances", "remote-projects",
  "app-server-project-id-by-legacy-project-id-by-host", "app-server-projects-migration-by-host",
  "thread-project-assignments", "sidebar-project-thread-orders",
]);

function clone(value) {
  return value === MISSING || value === undefined ? value : JSON.parse(JSON.stringify(value));
}
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function equal(a, b) { return a === b || isDeepStrictEqual(a, b); }
function own(value, key) { return Object.hasOwn(value, key) ? value[key] : MISSING; }
function put(value, key, next) {
  if (next === MISSING || next === undefined) delete value[key];
  else Object.defineProperty(value, key, { value: clone(next), enumerable: true, configurable: true, writable: true });
}
function snapshot(store) { return Object.fromEntries([...store.state].map(([key, value]) => [key, clone(value)])); }
function differences(before, after, prefix = [], result = []) {
  if (equal(before, after)) return result;
  if (object(before) && object(after)) {
    for (const key of new Set([...Object.keys(before), ...Object.keys(after)])) {
      differences(own(before, key), own(after, key), [...prefix, key], result);
    }
  } else result.push({ path: prefix, before: clone(before), after: clone(after) });
  return result;
}
function at(root, keys) {
  let value = root;
  for (const key of keys) {
    if (!object(value)) return MISSING;
    value = own(value, key);
  }
  return value;
}
function apply(root, operation) {
  const keys = operation.path;
  let parent = root;
  for (const key of keys.slice(0, -1)) {
    if (!object(own(parent, key))) return false;
    parent = parent[key];
  }
  put(parent, keys.at(-1), operation.after);
  return true;
}
function merge(root, operations) {
  const next = clone(root), conflicts = [];
  for (const operation of operations) {
    const current = at(next, operation.path);
    if (equal(current, operation.after)) continue;
    if (!equal(current, operation.before) || !apply(next, operation)) conflicts.push(operation);
  }
  return { next, conflicts };
}
function revision(value) { return crypto.createHash("sha256").update(JSON.stringify(value)).digest("hex"); }
function read(file) {
  let data;
  try { data = fs.readFileSync(file, "utf8"); }
  catch (error) { if (error.code === "ENOENT") return {}; throw error; }
  const value = JSON.parse(data);
  if (!object(value)) throw new Error("Shared global state must be a JSON object");
  return value;
}
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
function alive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return true;
  try { process.kill(pid, 0); return true; }
  catch (error) { return error.code !== "ESRCH"; }
}
function readOwner(directory) {
  try { return JSON.parse(fs.readFileSync(path.join(directory, "owner.json"), "utf8")); }
  catch { return null; }
}
async function acquire(file, { timeoutMs = 5000, retryMs = 25, isAlive = alive } = {}) {
  const directory = `${file}.csr-lock`;
  const owner = { version: 1, pid: process.pid, startedAt: Date.now() - Math.round(process.uptime() * 1000), nonce: crypto.randomUUID() };
  const deadline = Date.now() + timeoutMs;
  await fsp.mkdir(path.dirname(file), { recursive: true });
  for (;;) {
    try {
      await fsp.mkdir(directory);
      try { await fsp.writeFile(path.join(directory, "owner.json"), JSON.stringify(owner), { flag: "wx", mode: 0o600 }); }
      catch (error) { await fsp.rmdir(directory).catch(() => {}); throw error; }
      return async () => {
        if (readOwner(directory)?.nonce !== owner.nonce) return;
        await fsp.unlink(path.join(directory, "owner.json"));
        await fsp.rmdir(directory);
      };
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
    }
    const prior = readOwner(directory);
    if (prior?.nonce && Number.isInteger(prior.pid) && !isAlive(prior.pid)) {
      const claim = `${directory}.recovery`;
      let claimed = false;
      try {
        await fsp.mkdir(claim);
        claimed = true;
        const check = readOwner(directory);
        if (check?.nonce === prior.nonce && !isAlive(check.pid)) {
          await fsp.unlink(path.join(directory, "owner.json"));
          await fsp.rmdir(directory);
        }
      } catch (error) {
        if (error.code !== "EEXIST" && error.code !== "ENOENT") throw error;
      } finally { if (claimed) await fsp.rmdir(claim).catch(() => {}); }
    }
    if (Date.now() >= deadline) {
      const error = new Error("Timed out waiting for shared global-state ownership");
      error.code = "CSR_STATE_BUSY";
      throw error;
    }
    await delay(Math.min(retryMs, Math.max(1, deadline - Date.now())));
  }
}
async function atomicWrite(file, value, timeoutMs = 1000) {
  const temporary = `${file}.csr-tmp-${process.pid}-${crypto.randomUUID()}`;
  try {
    const handle = await fsp.open(temporary, "wx", 0o600);
    try { await handle.writeFile(JSON.stringify(value), "utf8"); await handle.sync(); }
    finally { await handle.close(); }
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      try { await fsp.rename(temporary, file); break; }
      catch (error) {
        if (!["EPERM", "EACCES", "EBUSY"].includes(error.code) || Date.now() >= deadline) throw error;
        await delay(25);
      }
    }
  } finally { await fsp.unlink(temporary).catch(() => {}); }
}

function install(store, options = {}) {
  if (store.__csrSharedGlobalState) return store.__csrSharedGlobalState;
  const file = store.filePath;
  const originalGet = store.get.bind(store), originalStored = store.getStored.bind(store);
  let baseline = snapshot(store), local = clone(baseline), observedRevision = revision(baseline);
  let operations = [], sequence = 0, queue = Promise.resolve(), timer = null, closed = false, watcher = null;
  let committing = false, lastError = null, lastReported = "";
  const provenance = new WeakMap(), listeners = new Set(), readBases = new Map(), externalBases = new Map();
  let explicitBase = null;
  const rejected = [];
  const warn = (message, error) => {
    try { store.logger?.().warning(message, { safe: { code: error?.code || "CSR_STATE_ERROR" }, sensitive: {} }); } catch {}
  };
  const emit = event => {
    for (const listener of [...listeners]) { try { listener(event); } catch (error) { warn("Shared state listener failed", error); } }
  };
  const notifyKeys = keys => {
    for (const key of keys) for (const callback of store.changeListeners?.get(key) || []) {
      try { callback(); } catch (error) { warn("Shared state listener failed", error); }
    }
  };
  const changedKeys = (a, b) => [...new Set([...Object.keys(a), ...Object.keys(b)])].filter(key => !equal(own(a, key), own(b, key)));
  const adopt = (disk, pending, reason, conflicts = []) => {
    const prior = local;
    if (reason === "external") for (const key of changedKeys(baseline, disk)) {
      if (!externalBases.has(key)) externalBases.set(key, clone(own(baseline, key)));
    }
    baseline = clone(disk);
    observedRevision = revision(disk);
    const rebased = merge(disk, pending);
    local = rebased.next;
    store.state = new Map(Object.entries(clone(local)));
    const keys = changedKeys(prior, local);
    notifyKeys(keys);
    if (keys.length || conflicts.length) emit({ keys, reason, conflicts: conflicts.map(op => op.path), revision: observedRevision });
  };
  const report = error => {
    lastError = error;
    const fingerprint = `${error.code || ""}:${error.message}:${observedRevision}:${sequence}`;
    if (lastReported === fingerprint) return;
    lastReported = fingerprint;
    warn("Shared desktop state could not be committed", error);
    emit({ keys: [], reason: "error", error, revision: observedRevision });
  };
  const saveConflict = async (conflicts, disk) => {
    if (!conflicts.length) return;
    const record = { version: 1, time: new Date().toISOString(), revision: revision(disk), operations: conflicts.map(op => ({ path: op.path, beforeMissing: op.before === MISSING, afterMissing: op.after === MISSING, ...(op.before === MISSING ? {} : { before: op.before }), ...(op.after === MISSING ? {} : { after: op.after }) })) };
    const directory = `${file}.csr-conflicts`;
    await fsp.mkdir(directory, { recursive: true });
    const target = path.join(directory, `${Date.now()}-${crypto.randomUUID()}.json`);
    await fsp.writeFile(target, JSON.stringify(record), { flag: "wx", mode: 0o600 });
    rejected.push(record);
    const error = new Error("Shared desktop state changed in another window; its current value was preserved");
    error.code = "CSR_STATE_CONFLICT";
    error.paths = conflicts.map(op => op.path);
    throw error;
  };
  const commit = async () => {
    if (!operations.length) return;
    const batch = operations.slice(), end = batch.at(-1).sequence;
    const release = await acquire(file, options.lock);
    committing = true;
    let committed = false;
    try {
      const disk = read(file), result = merge(disk, batch);
      // Preserve rejected intentions before acknowledging or discarding them.
      let conflictError = null;
      if (result.conflicts.length) {
        try { await saveConflict(result.conflicts, disk); }
        catch (error) { if (error.code !== "CSR_STATE_CONFLICT") throw error; conflictError = error; }
      }
      if (!equal(disk, result.next)) {
        await atomicWrite(`${file}.bak`, disk);
        await atomicWrite(file, result.next);
      }
      operations = operations.filter(op => op.sequence > end);
      committed = true;
      adopt(result.next, operations, "commit", result.conflicts);
      if (conflictError) throw conflictError;
      lastError = null; lastReported = "";
    } finally {
      committing = false;
      await release();
      if (!committed) { /* Pending operations remain retryable. */ }
    }
  };
  const enqueue = task => {
    const result = queue.catch(() => {}).then(task);
    queue = result;
    store.persistQueued = result.catch(() => {});
    return result;
  };
  const schedule = () => {
    if (closed || timer !== null) return;
    timer = setTimeout(() => {
      timer = null;
      enqueue(commit).catch(report);
    }, options.debounceMs ?? 25);
    timer.unref?.();
  };
  const remember = (key, value) => {
    const currentRead = { value: clone(own(local, key)) };
    readBases.set(key, currentRead);
    queueMicrotask(() => { if (readBases.get(key) === currentRead) readBases.delete(key); });
    if (value && typeof value === "object" && !provenance.has(value)) provenance.set(value, { key, snapshot: clone(own(local, key)) });
    return value;
  };
  store.get = key => remember(key, originalGet(key));
  store.getStored = key => remember(key, originalStored(key));
  store.set = (key, value) => {
    if (closed) throw new Error("Shared global-state store is closed");
    const origin = value && typeof value === "object" ? provenance.get(value) : null;
    const before = explicitBase?.key === key ? explicitBase.value : origin?.key === key ? origin.snapshot
      : readBases.has(key) ? readBases.get(key).value : externalBases.has(key) ? externalBases.get(key) : own(local, key);
    const next = value === undefined ? MISSING : clone(value);
    const delta = differences(before, next, [key]);
    if (!delta.length) return;
    const gen = ++sequence;
    operations.push(...delta.map(op => ({ ...op, sequence: gen })));
    // Update local state through CAS too: a held mutable reference may have
    // become stale while the external watcher replaced the live map.
    const result = merge(local, delta);
    local = result.next;
    store.state = new Map(Object.entries(clone(local)));
    notifyKeys([key]);
    schedule();
  };
  store.delete = key => store.set(key, undefined);
  store.update = (key, transform) => {
    const current = store.getStored(key);
    const before = clone(own(local, key));
    withBaseline(key, before, () => store.set(key, transform(current ?? null)));
  };
  function withBaseline(key, before, callback) {
    const prior = explicitBase;
    explicitBase = { key, value: clone(before) };
    try { return callback(); } finally { explicitBase = prior; }
  }
  store.updateAndPersist = (key, transform) => enqueue(async () => {
    await commit();
    const release = await acquire(file, options.lock);
    committing = true;
    try {
      const disk = read(file), before = own(disk, key);
      const result = transform(before === MISSING ? null : clone(before));
      if (result?.then) throw new Error("Shared state transaction callbacks must be synchronous");
      const next = clone(disk);
      put(next, key, result === undefined ? MISSING : result);
      if (!equal(disk, next)) { await atomicWrite(`${file}.bak`, disk); await atomicWrite(file, next); }
      adopt(next, operations, "transaction");
      lastError = null; lastReported = "";
    } finally { committing = false; await release(); }
  }).catch(error => { report(error); throw error; });
  store.flushOrThrow = () => {
    clearTimeout(timer); timer = null;
    return enqueue(async () => { await commit(); if (lastError) throw lastError; }).catch(error => { report(error); throw error; });
  };
  store.flush = () => store.flushOrThrow().catch(() => {});
  store.flushWith = () => store.flushOrThrow();
  store.persistForKey = schedule;
  store.queueGlobalStatePersistence = schedule;
  store.persistDebounced?.cancel?.();

  const refresh = () => {
    if (closed || committing) return false;
    try {
      const disk = read(file);
      if (revision(disk) === observedRevision) return false;
      adopt(disk, operations, "external");
      return true;
    } catch (error) { report(error); return false; }
  };
  let refreshTimer = null;
  const changed = () => {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(refresh, 40);
    refreshTimer.unref?.();
  };
  const poll = setInterval(refresh, options.pollMs ?? 1000);
  poll.unref?.();
  if (options.watch !== false) {
    try { watcher = fs.watch(path.dirname(file), { persistent: false }, (_event, name) => { if (name == null || String(name) === path.basename(file)) changed(); }); }
    catch (error) { warn("Shared state watcher unavailable; periodic refresh remains active", error); }
  }
  const api = {
    refresh,
    snapshot(key) { const value = own(local, key); return value === MISSING ? { present: false } : { present: true, value: clone(value) }; },
    withBaseline(key, expected, callback) {
      if (!expected || typeof expected.present !== "boolean" || expected.present && !Object.hasOwn(expected, "value")) {
        const error = new Error("Shared desktop write requires the renderer's original snapshot");
        error.code = "CSR_STATE_BASE_REQUIRED";
        report(error);
        emit({ keys: [key], reason: "rejected", conflicts: [[key]], revision: observedRevision });
        throw error;
      }
      return withBaseline(key, expected.present ? expected.value : MISSING, callback);
    },
    subscribe(listener) { listeners.add(listener); return () => listeners.delete(listener); },
    get revision() { return observedRevision; },
    get pendingCount() { return operations.length; },
    get lastError() { return lastError; },
    reportRefreshFailure(cause) {
      const error = new Error("Shared project cache could not be refreshed");
      error.code = "CSR_STATE_REFRESH_FAILED";
      error.cause = cause;
      report(error);
    },
    get conflicts() { return clone(rejected); },
    async close() { try { await store.flushOrThrow(); } finally { closed = true; clearTimeout(timer); clearTimeout(refreshTimer); clearInterval(poll); watcher?.close(); } },
  };
  Object.defineProperty(store, "__csrSharedGlobalState", { value: api });
  return api;
}

const bindings = new WeakMap(), projectRefreshes = new WeakMap(), shownErrors = new WeakSet();
function rendererSnapshot(handler, key) { return handler.globalState.__csrSharedGlobalState.snapshot(key); }
async function setRendererGlobal(handler, key, value, expected) {
  const api = handler.globalState.__csrSharedGlobalState;
  api.withBaseline(key, expected, () => handler.setGlobalStateValue(key, value));
  await handler.globalState.flushOrThrow();
  return { success: true };
}
function setRendererAtom(handler, contents, key, value, recordUpdate, expected, updateRecord) {
  const store = handler.globalState, api = store.__csrSharedGlobalState;
  try {
    // The snapshot covers one atom; build a baseline container using current
    // siblings so unrelated atoms are never interpreted as renderer changes.
    const current = api.snapshot(ATOMS), before = clone(current.present ? current.value : {});
    if (!expected || typeof expected.present !== "boolean" || expected.present && !Object.hasOwn(expected, "value")) api.withBaseline(ATOMS, null, () => {});
    put(before, key, expected.present ? expected.value : MISSING);
    const next = clone(before);
    const updated = recordUpdate == null ? value : updateRecord(clone(before[key] ?? (recordUpdate.legacyStorageKey == null ? undefined : before[recordUpdate.legacyStorageKey])), recordUpdate);
    put(next, key, updated === undefined ? MISSING : updated);
    api.withBaseline(ATOMS, current.present || expected.present ? { present: true, value: before } : { present: false }, () => store.set(ATOMS, next));
    // Awaited by the patched event dispatcher; sync every window after CAS.
    return store.flushOrThrow().then(() => handler.broadcastPersistedAtomUpdate(contents, key, store.get(ATOMS)?.[key])).catch(() => { handler.sendPersistedAtomState(contents); });
  } catch { handler.sendPersistedAtomState(contents); }
}
function bindRenderer(handler, contents, showError) {
  const coordinator = handler.globalState?.__csrSharedGlobalState;
  if (!coordinator || !contents || bindings.has(contents)) return;
  const stop = coordinator.subscribe(event => {
    if (contents.isDestroyed?.()) return;
    if (event.keys.length) handler.sendMessageToView(contents, { type: "global-state-updated", keys: event.keys.filter(key => key !== ATOMS) });
    if (event.keys.includes(ATOMS) || event.conflicts?.length) handler.sendPersistedAtomState(contents);
    if (event.keys.some(key => PROJECT_KEYS.has(key))) {
      handler.sendMessageToView(contents, { type: "workspace-root-options-updated" });
      const backend = handler.projectsManager?.projectBackend;
      if (backend?.ensureProjectsReady && !projectRefreshes.has(backend)) {
        const job = Promise.resolve(backend.projectInitialization).catch(() => {}).then(() => {
          backend.projectsReady = false;
          return backend.ensureProjectsReady();
        }).catch(error => { coordinator.reportRefreshFailure(error); }).finally(() => projectRefreshes.delete(backend));
        projectRefreshes.set(backend, job);
      }
      const refresh = projectRefreshes.get(backend);
      if (refresh) void refresh.then(() => {
        if (contents.isDestroyed?.()) return;
        handler.sendMessageToView(contents, { type: "global-state-updated", keys: event.keys.filter(key => PROJECT_KEYS.has(key)) });
        handler.sendMessageToView(contents, { type: "workspace-root-options-updated" });
      }).catch(error => coordinator.reportRefreshFailure(error));
    }
    if (event.error && !shownErrors.has(event)) {
      shownErrors.add(event);
      showError?.(event.error.code === "CSR_STATE_CONFLICT"
        ? "Los datos cambiaron en otra ventana. Se conservó la versión compartida y se guardó el cambio rechazado para recuperarlo. Repite el cambio si todavía lo necesitas."
        : event.error.code === "CSR_STATE_BASE_REQUIRED"
          ? "La ventana no tenía una copia actualizada de los datos. Se ha solicitado una actualización; repite el cambio."
          : event.error.code === "CSR_STATE_REFRESH_FAILED"
            ? "Los datos están guardados, pero no se pudo actualizar la lista de proyectos. Vuelve a abrir esta ventana para recargarla."
            : "No se pudieron guardar los cambios compartidos. Los cambios siguen pendientes; vuelve a intentarlo cuando termine la otra operación.");
    }
  });
  bindings.set(contents, stop);
  contents.once?.("destroyed", () => { stop(); bindings.delete(contents); });
}

module.exports = { install, bindRenderer, rendererSnapshot, setRendererGlobal, setRendererAtom, acquire, atomicWrite, read, differences, merge, MISSING };

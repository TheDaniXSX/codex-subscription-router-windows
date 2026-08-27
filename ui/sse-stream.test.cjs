const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const accountMenuPath = path.join(__dirname, "account-menu.js");
const threadSubscriptionPath = path.join(__dirname, "thread-subscription.js");
const accountMenuSource = fs.readFileSync(accountMenuPath, "utf8");
const threadSubscriptionSource = fs.readFileSync(threadSubscriptionPath, "utf8");

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

function responseFromChunks(chunks) {
  const encoded = chunks.map((chunk) => new TextEncoder().encode(chunk));
  let index = 0;
  return {
    ok: true,
    status: 200,
    headers: { get: () => "text/event-stream; charset=utf-8" },
    body: {
      getReader: () => ({
        read: async () =>
          index < encoded.length
            ? { done: false, value: encoded[index++] }
            : { done: true, value: undefined },
        releaseLock: () => {},
      }),
    },
  };
}

async function waitFor(predicate, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  assert.fail(message);
}

test("authenticated SSE uses a header and parses chunked CRLF events", async () => {
  const requests = [];
  const token = "a".repeat(64);
  const context = loadAccountMenu({
    fetch: async (url, options) => {
      requests.push({ url, options });
      return responseFromChunks([
        "\uFEFF: keepalive\r",
        "\nretry: 2500\r\ndata: {\"type\":\"account-",
        "updated\"}\r\nid: account-event-7\r\n\r\n",
        "data: first line\ndata: second line\n\n",
      ]);
    },
  });

  const events = [];
  let stop = () => {};
  stop = context.codexMuxSubscribeEvents({
    apiBase: "http://127.0.0.1:49152/v1",
    token,
    onMessage: (event) => {
      events.push({
        data: event.data,
        lastEventId: event.lastEventId,
        type: event.type,
      });
      if (events.length === 2) stop();
    },
  });

  await waitFor(() => events.length === 2, "expected two parsed SSE messages");
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, "http://127.0.0.1:49152/v1/events");
  assert.equal(requests[0].url.includes(token), false);
  assert.equal(requests[0].url.includes("token="), false);
  assert.equal(requests[0].options.headers["X-Codex-Mux-Token"], token);
  assert.equal(requests[0].options.headers.Accept, "text/event-stream");
  assert.equal(requests[0].options.credentials, "omit");
  assert.equal(requests[0].options.cache, "no-store");
  assert.deepEqual(events, [
    {
      data: '{"type":"account-updated"}',
      lastEventId: "account-event-7",
      type: "message",
    },
    {
      data: "first line\nsecond line",
      lastEventId: "account-event-7",
      type: "message",
    },
  ]);
});

test("SSE reconnects after failure and cancellation aborts the active fetch", async () => {
  let fetchCount = 0;
  let activeSignal = null;
  let nextTimer = 0;
  const timers = new Map();
  const context = loadAccountMenu({
    setTimeout: (callback) => {
      const id = ++nextTimer;
      timers.set(id, callback);
      queueMicrotask(() => {
        if (!timers.delete(id)) return;
        callback();
      });
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
    fetch: async (_url, options) => {
      fetchCount += 1;
      if (fetchCount === 1) throw new Error("temporary connection failure");
      activeSignal = options.signal;
      return {
        ok: true,
        status: 200,
        headers: { get: () => "text/event-stream" },
        body: {
          getReader: () => ({
            read: () =>
              new Promise((_resolve, reject) => {
                options.signal.addEventListener(
                  "abort",
                  () => {
                    const error = new Error("aborted");
                    error.name = "AbortError";
                    reject(error);
                  },
                  { once: true },
                );
              }),
            releaseLock: () => {},
          }),
        },
      };
    },
  });

  const stop = context.codexMuxSubscribeEvents({
    apiBase: "http://127.0.0.1:49153/v1",
    token: "b".repeat(64),
    onMessage: () => {},
  });
  await waitFor(() => fetchCount === 2, "expected the SSE connection to retry");
  assert.equal(activeSignal.aborted, false);
  stop();
  assert.equal(activeSignal.aborted, true);
  stop();
  assert.equal(fetchCount, 2);
});

test("SSE 204 response ends the subscription without reconnecting", async () => {
  let fetchCount = 0;
  let timerCount = 0;
  const context = loadAccountMenu({
    setTimeout: () => {
      timerCount += 1;
      return timerCount;
    },
    clearTimeout: () => {},
    fetch: async () => {
      fetchCount += 1;
      return {
        ok: true,
        status: 204,
        headers: { get: () => "" },
        body: null,
      };
    },
  });

  const stop = context.codexMuxSubscribeEvents({
    apiBase: "http://127.0.0.1:49155/v1",
    token: "c".repeat(64),
    onMessage: () => assert.fail("204 must not emit events"),
  });
  await waitFor(() => fetchCount === 1, "expected the SSE request");
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(fetchCount, 1);
  assert.equal(timerCount, 0);
  stop();
});

test("SSE rejects an invalid content type and reconnects", async () => {
  let fetchCount = 0;
  let nextTimer = 0;
  const timers = new Map();
  const context = loadAccountMenu({
    setTimeout: (callback) => {
      const id = ++nextTimer;
      timers.set(id, callback);
      queueMicrotask(() => {
        if (!timers.delete(id)) return;
        callback();
      });
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
    fetch: async () => {
      fetchCount += 1;
      if (fetchCount === 1) {
        return {
          ok: true,
          status: 200,
          headers: { get: () => "application/json" },
          body: responseFromChunks([]).body,
        };
      }
      return {
        ok: true,
        status: 204,
        headers: { get: () => "" },
        body: null,
      };
    },
  });

  const stop = context.codexMuxSubscribeEvents({
    apiBase: "http://127.0.0.1:49156/v1",
    token: "d".repeat(64),
    onMessage: () => assert.fail("invalid stream must not emit events"),
  });
  await waitFor(() => fetchCount === 2, "expected retry after invalid content type");

  stop();
  assert.equal(fetchCount, 2);
});

test("SSE retry hint is clamped to the configured maximum", async () => {
  const delays = [];
  let fetchCount = 0;
  let stop = () => {};
  const context = loadAccountMenu({
    setTimeout: (_callback, delay) => {
      delays.push(delay);
      return delays.length;
    },
    clearTimeout: () => {},
    fetch: async () => {
      fetchCount += 1;
      return responseFromChunks(["retry: 999999\ndata: {}\n\n"]);
    },
  });

  stop = context.codexMuxSubscribeEvents({
    apiBase: "http://127.0.0.1:49157/v1",
    token: "e".repeat(64),
    onMessage: () => {},
  });
  await waitFor(() => delays.length === 1, "expected a reconnect timer");

  assert.equal(fetchCount, 1);
  assert.deepEqual(delays, [30_000]);
  stop();
});

test("SSE enforces the maximum event and line size", async () => {
  const context = loadAccountMenu();
  const abortController = new AbortController();
  const oversizedLine = `data: ${"x".repeat(256 * 1024 + 1)}\n\n`;

  await assert.rejects(
    context.codexMuxReadEventStream(
      responseFromChunks([oversizedLine]).body,
      () => assert.fail("oversized event must not be emitted"),
      abortController.signal,
      () => {},
    ),
    /too large/,
  );
});

test("SSE size limits count UTF-8 bytes rather than UTF-16 code units", async () => {
  const context = loadAccountMenu({ TextEncoder });
  const abortController = new AbortController();
  // U+1F600 occupies four UTF-8 bytes but only two UTF-16 code units. This
  // remains below the character limit while exceeding the transport limit.
  const oversizedUtf8Line = `data: ${"😀".repeat(70_000)}\n\n`;

  await assert.rejects(
    context.codexMuxReadEventStream(
      responseFromChunks([oversizedUtf8Line]).body,
      () => assert.fail("oversized UTF-8 event must not be emitted"),
      abortController.signal,
      () => {},
    ),
    /too large/,
  );
});

test("aborting the SSE reader cancels and releases the underlying stream", async () => {
  const context = loadAccountMenu();
  const abortController = new AbortController();
  let finishRead = null;
  let cancelCount = 0;
  let releaseCount = 0;
  const body = {
    getReader: () => ({
      read: () =>
        new Promise((resolve) => {
          finishRead = resolve;
        }),
      cancel: () => {
        cancelCount += 1;
        finishRead?.({ done: true, value: undefined });
      },
      releaseLock: () => {
        releaseCount += 1;
      },
    }),
  };

  const reading = context.codexMuxReadEventStream(
    body,
    () => assert.fail("cancelled stream must not emit an event"),
    abortController.signal,
    () => {},
  );
  await waitFor(() => finishRead !== null, "expected an active reader");
  abortController.abort();
  await reading;

  assert.equal(cancelCount, 1);
  assert.equal(releaseCount, 1);
});

test("thread subscription uses the shared authenticated stream and closes it", () => {
  let effectCleanup = null;
  let streamOptions = null;
  let streamCloseCount = 0;
  let fetchCount = 0;
  const context = vm.createContext({
    AbortController,
    CODEX_MUX_CONTROL_PORT: 49154,
    TE: {
      useEffect: (effect) => {
        effectCleanup = effect();
      },
      useState: () => [null, () => {}],
    },
    clearInterval: () => {},
    clearTimeout: () => {},
    fetch: async () => {
      fetchCount += 1;
      return { ok: true, json: async () => ({ account: null }) };
    },
    globalThis: undefined,
    setInterval: () => 2,
    setTimeout: () => 1,
    sr: {},
    $n: () => ({
      value: { routeKind: "local-thread", conversationId: "thread-7" },
    }),
  });
  context.globalThis = context;
  context.codexMuxSubscribeEvents = (options) => {
    streamOptions = options;
    return () => {
      streamCloseCount += 1;
    };
  };
  vm.runInContext(threadSubscriptionSource, context, {
    filename: threadSubscriptionPath,
  });

  assert.equal(context.CodexMuxThreadSubscription(), null);
  assert.equal(fetchCount, 1);
  assert.equal(
    streamOptions.apiBase,
    "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1",
  );
  assert.equal(streamOptions.token, "__CODEX_MUX_CONTROL_TOKEN__");

  streamOptions.onMessage({
    data: JSON.stringify({
      type: "thread-failed-over",
      data: { threadId: "another-thread" },
    }),
  });
  assert.equal(fetchCount, 1);
  streamOptions.onMessage({
    data: JSON.stringify({
      type: "thread-failed-over",
      data: { threadId: "thread-7" },
    }),
  });
  assert.equal(fetchCount, 2);

  effectCleanup();
  assert.equal(streamCloseCount, 1);
});

test("renderer sources contain no EventSource or tokenized event URL", () => {
  for (const source of [accountMenuSource, threadSubscriptionSource]) {
    assert.doesNotMatch(source, /\bEventSource\b/);
    assert.doesNotMatch(source, /events\?token=/);
    assert.doesNotMatch(source, /console\.(?:log|info|warn|error).*TOKEN/i);
  }
});

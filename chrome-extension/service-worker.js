const NATIVE_HOST = "io.github.thedanixsx.codex_subscription_router";
const PROTOCOL = "codex-router-native-v1";

function requestId() {
  return crypto.randomUUID();
}

async function sendNative(type, payload) {
  const message = { protocol: PROTOCOL, id: requestId(), type };
  if (payload !== undefined) {
    message.payload = payload;
  }
  const response = await chrome.runtime.sendNativeMessage(NATIVE_HOST, message);
  if (!response || response.protocol !== PROTOCOL || response.id !== message.id) {
    throw new Error("The native host returned an invalid response.");
  }
  if (!response.ok) {
    throw new Error(response.error?.message || "The native host rejected the request.");
  }
  return response.result;
}

async function captureActiveTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id || !/^https?:\/\//u.test(tab.url || "")) {
    throw new Error("Only active HTTP or HTTPS pages can be shared.");
  }
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => {
      const selection = window.getSelection()?.toString() || "";
      const text = document.body?.innerText || "";
      return {
        url: location.href.slice(0, 16384),
        title: document.title.slice(0, 4096),
        selection: selection.slice(0, 65536),
        text: text.slice(0, 262144),
      };
    },
  });
  if (!result) {
    throw new Error("The active page did not return browser context.");
  }
  return sendNative("browser.context.publish", result);
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  const operation = (() => {
    switch (message?.type) {
      case "connector.status":
        return sendNative("hello");
      case "router.health":
        return sendNative("router.health");
      case "browser.context.capture":
        return captureActiveTab();
      default:
        return Promise.reject(new Error("Unsupported extension request."));
    }
  })();
  operation.then(
    (result) => sendResponse({ ok: true, result }),
    (error) => sendResponse({ ok: false, error: String(error?.message || error) }),
  );
  return true;
});

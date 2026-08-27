const status = document.querySelector("#status");
const share = document.querySelector("#share");

function call(type) {
  return chrome.runtime.sendMessage({ type });
}

async function refresh() {
  const response = await call("connector.status");
  if (!response?.ok) {
    status.textContent = response?.error || "Native connector unavailable.";
    status.dataset.state = "error";
    share.disabled = true;
    return;
  }
  status.textContent = "Native connector ready.";
  status.dataset.state = "ready";
  share.disabled = false;
}

share.addEventListener("click", async () => {
  share.disabled = true;
  status.textContent = "Capturing this page…";
  const response = await call("browser.context.capture");
  if (response?.ok) {
    status.textContent = "Page context saved locally. Desktop import is still release-gated.";
    status.dataset.state = "ready";
  } else {
    status.textContent = response?.error || "The page could not be shared.";
    status.dataset.state = "error";
  }
  share.disabled = false;
});

refresh().catch((error) => {
  status.textContent = error?.message || "Native connector unavailable.";
  status.dataset.state = "error";
});

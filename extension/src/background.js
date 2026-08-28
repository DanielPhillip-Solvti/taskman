// Service worker: the only piece that talks to taskmand. The content
// script never fetches directly — it messages this worker, which holds the
// daemon base URL and does the actual HTTP call. Keeps a single point of
// contact if auth is ever added later.

const DAEMON = "http://127.0.0.1:8765";

async function call(path, opts) {
  let res;
  try {
    res = await fetch(DAEMON + path, opts);
  } catch (err) {
    // Most likely cause: taskmand isn't running. Surface that plainly
    // rather than letting the content script guess from a generic
    // "Failed to fetch".
    return { ok: false, status: 0, body: { error: `Could not reach taskmand at ${DAEMON} (${err.message}). Is it running?` } };
  }
  let body = null;
  try {
    body = await res.json();
  } catch {
    // No/invalid JSON body — leave body null, status still tells the story.
  }
  return { ok: res.ok, status: res.status, body };
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    switch (msg.type) {
      case "getConfig":
        sendResponse(await call("/config"));
        break;
      case "setHarness":
        sendResponse(await call("/config/harness", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ harness: msg.harness }) }));
        break;
      case "setModel":
        sendResponse(await call("/config/model", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ model: msg.model }) }));
        break;
      case "listRepos":
        sendResponse(await call("/repos"));
        break;
      case "fetchRepo":
        sendResponse(await call("/repos/fetch", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ url: msg.url, odoo_version: msg.odooVersion }) }));
        break;
      case "refineTask":
        sendResponse(await call(`/tasks/${msg.number}/refine`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ repo_name: msg.repoName, description: msg.description }) }));
        break;
      case "implementTask":
        sendResponse(await call(`/tasks/${msg.number}/implement`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ repo_name: msg.repoName, description: msg.description }) }));
        break;
      case "taskOutput":
        sendResponse(await call(`/tasks/${msg.number}/output`));
        break;
      case "interruptTask":
        sendResponse(await call(`/tasks/${msg.number}/interrupt`, { method: "POST" }));
        break;
      default:
        sendResponse({ ok: false, status: 0, body: { error: `background: unknown message type ${msg.type}` } });
    }
  })();
  return true; // keep the message channel open for the async response
});

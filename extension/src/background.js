// Service worker: the only piece that talks to taskmand. The content
// script never fetches directly — it messages this worker, which holds the
// daemon base URL and does the actual HTTP call. Keeps a single point of
// contact if auth is ever added later.

const DAEMON = "http://127.0.0.1:8765";

// Reflects daemon reachability on the toolbar icon so "it's just not
// installed/running" is visible without opening a task page: a red badge
// means the last call to taskmand couldn't even reach it. Cleared on any
// successful round-trip (any HTTP response at all, error status or not).
function setUnreachable(unreachable) {
  chrome.action.setBadgeText({ text: unreachable ? "!" : "" });
  chrome.action.setBadgeBackgroundColor({ color: "#bf616a" });
}

async function call(path, opts) {
  let res;
  try {
    res = await fetch(DAEMON + path, opts);
  } catch (err) {
    // Most likely cause: taskmand isn't running. Surface that plainly
    // rather than letting the content script guess from a generic
    // "Failed to fetch".
    setUnreachable(true);
    return { ok: false, status: 0, body: { error: `Could not reach taskmand at ${DAEMON} (${err.message}). Click the taskman toolbar icon to install/start it.` } };
  }
  setUnreachable(false);
  let body = null;
  try {
    body = await res.json();
  } catch {
    // No/invalid JSON body — leave body null, status still tells the story.
  }
  return { ok: res.ok, status: res.status, body };
}

// Checked on install/browser start so the badge reflects reality even
// before the extension is used on a task page.
chrome.runtime.onInstalled.addListener(() => call("/health"));
chrome.runtime.onStartup.addListener(() => call("/health"));

// Odoo's session_id cookie is HttpOnly (content scripts can't read it via
// document.cookie), so this — the background service worker, which holds
// the "cookies" permission — is the only place it can be fetched from.
async function getSessionID(host) {
  const cookie = await chrome.cookies.get({ url: host, name: "session_id" });
  if (!cookie) {
    throw new Error(`No Odoo session cookie found for ${host} — are you logged in there?`);
  }
  return cookie.value;
}

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  (async () => {
    switch (msg.type) {
      case "checkHealth":
        sendResponse(await call("/health"));
        break;
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
        try {
          const sessionId = await getSessionID(msg.host);
          sendResponse(await call(`/tasks/${msg.number}/refine`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ repo_name: msg.repoName, host: msg.host, session_id: sessionId }) }));
        } catch (err) {
          sendResponse({ ok: false, status: 0, body: { error: err.message } });
        }
        break;
      case "implementTask":
        try {
          const sessionId = await getSessionID(msg.host);
          sendResponse(await call(`/tasks/${msg.number}/implement`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ repo_name: msg.repoName, host: msg.host, session_id: sessionId }) }));
        } catch (err) {
          sendResponse({ ok: false, status: 0, body: { error: err.message } });
        }
        break;
      case "taskOutput":
        sendResponse(await call(`/tasks/${msg.number}/output`));
        break;
      case "interruptTask":
        sendResponse(await call(`/tasks/${msg.number}/interrupt`, { method: "POST" }));
        break;
      case "openOptionsPage":
        // Stash the scraped project name for options.js to pick up and
        // prefill — chrome.runtime.openOptionsPage() takes no arguments,
        // so storage is the only channel available to pass it along.
        if (msg.projectName) {
          await chrome.storage.local.set({ taskmanPendingProject: msg.projectName });
        }
        chrome.runtime.openOptionsPage();
        sendResponse({ ok: true, status: 0, body: null });
        break;
      default:
        sendResponse({ ok: false, status: 0, body: { error: `background: unknown message type ${msg.type}` } });
    }
  })();
  return true; // keep the message channel open for the async response
});

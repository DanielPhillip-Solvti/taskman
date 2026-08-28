// Injects a small button bar onto a project.task form page. Dumb by
// design: scrape (scrape.js), render buttons, call the daemon through the
// background service worker, write drafts back into the DOM. No Odoo API
// calls, no state of its own beyond the project->repo mapping in
// chrome.storage.local.

const POLL_INTERVAL_MS = 3000;

function sendToBackground(msg) {
  return chrome.runtime.sendMessage(msg);
}

async function getMapping(projectName) {
  const store = await chrome.storage.local.get(["projectMap"]);
  const map = store.projectMap || {};
  return map[projectName] || null;
}

function buildBar() {
  const host = document.createElement("div");
  host.id = "taskman-bar-host";
  host.style.position = "fixed";
  host.style.top = "8px";
  host.style.right = "8px";
  host.style.zIndex = "100000";
  const shadow = host.attachShadow({ mode: "open" });

  const style = document.createElement("style");
  style.textContent = `
    .bar { font-family: system-ui, sans-serif; background: #1f2430; color: #fff;
           border-radius: 8px; padding: 8px 10px; display: flex; gap: 6px;
           align-items: center; box-shadow: 0 2px 8px rgba(0,0,0,.3); font-size: 12px; }
    button { font: inherit; background: #3b4252; color: #fff; border: none;
             border-radius: 5px; padding: 5px 9px; cursor: pointer; }
    button:hover { background: #4c566a; }
    button:disabled { opacity: .5; cursor: default; }
    .status { opacity: .8; max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .panel { position: fixed; top: 44px; right: 8px; width: 480px; max-height: 60vh;
             overflow: auto; background: #1f2430; color: #d8dee9; padding: 10px;
             border-radius: 8px; font: 11px/1.4 ui-monospace, monospace; white-space: pre-wrap;
             box-shadow: 0 2px 8px rgba(0,0,0,.3); display: none; }
  `;
  shadow.appendChild(style);

  const bar = document.createElement("div");
  bar.className = "bar taskman-";
  shadow.appendChild(bar);

  const panel = document.createElement("div");
  panel.className = "panel";
  shadow.appendChild(panel);

  document.body.appendChild(host);
  return { bar, panel };
}

function button(label, onClick) {
  const b = document.createElement("button");
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

async function main() {
  const scraped = window.__taskmanScrape();
  if (!scraped) return; // not a task page

  const { bar, panel } = buildBar();
  const status = document.createElement("span");
  status.className = "status";
  status.textContent = `#${scraped.taskNumber}`;
  bar.appendChild(status);

  const mapping = await getMapping(scraped.projectName);

  if (!mapping) {
    status.textContent = `#${scraped.taskNumber} — "${scraped.projectName}" not mapped`;
    bar.appendChild(button("Configure…", () => chrome.runtime.openOptionsPage()));
    return;
  }

  let polling = null;

  function showPanel(text) {
    panel.style.display = "block";
    panel.textContent = text;
    panel.scrollTop = panel.scrollHeight;
  }

  function stopPolling() {
    if (polling) clearInterval(polling);
    polling = null;
  }

  async function pollOnce() {
    const res = await sendToBackground({ type: "taskOutput", number: scraped.taskNumber });
    if (!res.ok) {
      showPanel(`[taskman] ${res.body?.error || "could not fetch task output"}`);
      return;
    }
    showPanel(`status: ${res.body.status}\n\n${res.body.log}`);
    if (res.body.status !== "queued" && res.body.status !== "running") {
      stopPolling();
    }
  }

  function startPolling() {
    stopPolling();
    pollOnce();
    polling = setInterval(pollOnce, POLL_INTERVAL_MS);
  }

  async function ensureRepoFetched() {
    const fetchRes = await sendToBackground({ type: "fetchRepo", url: mapping.repoUrl, odooVersion: mapping.odooVersion });
    if (!fetchRes.ok) {
      throw new Error(fetchRes.body?.error || "fetchRepo failed");
    }
    return fetchRes.body.repo.name;
  }

  const refineBtn = button("Refine", async () => {
    refineBtn.disabled = true;
    try {
      const repoName = await ensureRepoFetched();
      const res = await sendToBackground({
        type: "refineTask",
        number: scraped.taskNumber,
        repoName,
        title: scraped.title,
        description: scraped.descriptionHtml,
      });
      if (!res.ok) {
        showPanel(`[taskman] ${res.body?.error || "refine failed to start"}`);
      } else {
        startPolling();
      }
    } catch (err) {
      showPanel(`[taskman] ${err.message}`);
    } finally {
      refineBtn.disabled = false;
    }
  });
  bar.appendChild(refineBtn);

  const implementBtn = button("Implement", async () => {
    implementBtn.disabled = true;
    try {
      const repoName = await ensureRepoFetched();
      const res = await sendToBackground({
        type: "implementTask",
        number: scraped.taskNumber,
        repoName,
        title: scraped.title,
        description: scraped.descriptionHtml,
      });
      if (!res.ok) {
        showPanel(`[taskman] ${res.body?.error || "implement failed to start"}`);
      } else {
        startPolling();
      }
    } catch (err) {
      showPanel(`[taskman] ${err.message}`);
    } finally {
      implementBtn.disabled = false;
    }
  });
  bar.appendChild(implementBtn);

  bar.appendChild(button("Monitor", () => {
    if (panel.style.display === "block") {
      panel.style.display = "none";
    } else {
      startPolling();
    }
  }));

  bar.appendChild(button("Stop", async () => {
    const res = await sendToBackground({ type: "interruptTask", number: scraped.taskNumber });
    if (!res.ok) showPanel(`[taskman] ${res.body?.error || "interrupt failed"}`);
  }));
}

main().catch((err) => console.error("[taskman] content script error:", err));

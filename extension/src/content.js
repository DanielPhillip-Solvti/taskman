// Injects a taskman systray icon onto a project.task form page; clicking it
// opens a modal with the Refine/Implement/Stop actions and the agent's
// output. Dumb by design: scrape (scrape.js), render, call the daemon
// through the background service worker. No Odoo API calls, no state of
// its own beyond the project->repo mapping in chrome.storage.local.
// Rendering the agent's log/answer into readable sections is panel.js's
// job — this file just wires that into the DOM.
//
// Odoo is a single-page app (Owl framework): the task form's markup is not
// present at document_idle — it renders asynchronously after that, and
// navigating between records (breadcrumbs, the pager, opening another
// task) never triggers a full page load / fresh content-script injection
// at all. So this can't be a one-shot "scrape once at load" script — it
// watches the DOM for as long as the tab is open and (re)renders the icon
// whenever the visible task changes.

const POLL_INTERVAL_MS = 3000;
const OBSERVER_DEBOUNCE_MS = 150;

const STATUS_LABELS = {
  queued: "Queued",
  running: "Running",
  done: "Done",
  failed: "Failed",
  interrupted: "Interrupted",
};

function sendToBackground(msg) {
  return chrome.runtime.sendMessage(msg);
}

async function getMapping(projectName) {
  const store = await chrome.storage.local.get(["projectMap"]);
  const map = store.projectMap || {};
  return map[projectName] || null;
}

// Statuses that mean "a task is currently being handled" — refine/implement
// must not be re-triggered while true, and Stop is only meaningful then.
const IN_FLIGHT_STATUSES = new Set(["queued", "running"]);

// The systray icon is the sole entry point into the panel: clicking it
// toggles the modal open/closed. There's no floating bar competing for the
// same top-right corner Odoo docks its own systray in (o_menu_systray).
function injectSystrayToggle(onToggle) {
  const systray = document.querySelector(".o_menu_systray");
  if (!systray) return;

  let toggle = systray.querySelector("#taskman-systray-toggle");
  if (toggle) {
    // Systray survives across Owl re-renders of the task form; just rebind
    // the existing icon to whatever handler is currently live.
    toggle.onclick = onToggle;
    return;
  }

  toggle = document.createElement("button");
  toggle.id = "taskman-systray-toggle";
  toggle.title = "Open taskman panel";
  toggle.textContent = "🤖";
  toggle.className = "o-dropdown dropdown o-no-caret";
  toggle.style.border = "none";
  toggle.style.background = "transparent";
  toggle.style.cursor = "pointer";
  toggle.style.fontSize = "16px";
  toggle.style.padding = "0 8px";
  toggle.onclick = onToggle;
  systray.prepend(toggle);
}

function buildBarHost() {
  const host = document.createElement("div");
  host.id = "taskman-bar-host";
  host.style.position = "fixed";
  host.style.top = "8px";
  host.style.right = "8px";
  host.style.zIndex = "100000";
  // The host box itself spans down to the bar/modal regardless of margins
  // on its children (margin-collapse leaves an invisible sliver at the top
  // of the host that still intercepts clicks). Make the host click-through
  // and only re-enable pointer events on the actual visible bar/overlay, so
  // whatever's underneath — like the systray — stays clickable.
  host.style.pointerEvents = "none";
  const shadow = host.attachShadow({ mode: "open" });

  const style = document.createElement("style");
  style.textContent = `
    :host, * { box-sizing: border-box; }
    button { font: inherit; background: #3b4252; color: #fff; border: none;
             border-radius: 5px; padding: 5px 9px; cursor: pointer; }
    button:hover { background: #4c566a; }
    button:disabled { opacity: .5; cursor: default; }

    /* --- modal --- */
    .overlay { position: fixed; inset: 0; background: rgba(10,12,18,.55);
               display: none; align-items: center; justify-content: center;
               pointer-events: auto; font-family: system-ui, sans-serif; }
    .overlay.open { display: flex; }
    .modal { width: 80vw; height: 80vh; background: #1f2430;
             color: #e5e9f0; border-radius: 14px; box-shadow: 0 20px 60px rgba(0,0,0,.5);
             display: flex; flex-direction: column; overflow: hidden; }
    .modal-header { display: flex; align-items: center; gap: 10px; padding: 14px 18px;
                    border-bottom: 1px solid #3b4252; flex-shrink: 0; }
    .modal-title { font-weight: 600; font-size: 14px; flex: 1; }
    .badge { font-size: 10px; padding: 3px 9px; border-radius: 999px; text-transform: uppercase;
             letter-spacing: .04em; font-weight: 700; white-space: nowrap; }
    .badge-queued { background: #4c566a; color: #eceff4; }
    .badge-running { background: #5e81ac; color: #fff; }
    .badge-done { background: #3d8b5f; color: #fff; }
    .badge-failed { background: #bf616a; color: #fff; }
    .badge-interrupted { background: #d08770; color: #fff; }
    .icon-btn { background: transparent; border: none; color: #d8dee9; font-size: 16px;
                line-height: 1; cursor: pointer; padding: 4px 8px; border-radius: 6px; }
    .icon-btn:hover { background: #3b4252; }
    .modal-body { overflow: auto; padding: 16px 18px; display: flex; flex-direction: column; gap: 14px; flex: 1; min-height: 0; }
    .toolbar { display: flex; gap: 6px; align-items: center; }
    .toolbar .spacer { flex: 1; }
    .toolbar .hint { opacity: .6; font-size: 11px; }
    button.stop { background: #7a4148; }
    button.stop:hover { background: #bf616a; }
    .section { background: #262b38; border-radius: 10px; padding: 12px 14px; }
    .section h3 { margin: 0 0 8px; font-size: 11px; text-transform: uppercase;
                  letter-spacing: .05em; color: #88c0d0; }
    .section.kind-acceptance h3 { color: #a3be8c; }
    .section.kind-questions h3 { color: #ebcb8b; }
    .section p { margin: 0 0 8px; font-size: 13px; line-height: 1.55; white-space: pre-wrap; }
    .section p:last-child { margin-bottom: 0; }
    .section ul { margin: 0 0 8px 18px; padding: 0; font-size: 13px; line-height: 1.55; }
    .section ul:last-child { margin-bottom: 0; }
    .stream-toggle { cursor: pointer; user-select: none; }
    .stream { background: #11151c; border-radius: 10px; padding: 10px 12px;
              font: 12px/1.55 ui-monospace, monospace; white-space: pre-wrap;
              max-height: 60vh; overflow: auto; }
    .stream div.tool-call { color: #88c0d0; }
    .stream div.tool-result { color: #7d889b; padding-left: 14px; }
    .stream div.divider { color: #4c566a; }
    .stream div.error { color: #bf616a; font-weight: 600; }
    .empty-note { opacity: .6; font-size: 12px; padding: 4px 2px; }
  `;
  shadow.appendChild(style);

  const overlay = document.createElement("div");
  overlay.className = "overlay";
  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) overlay.classList.remove("open"); // click-outside-to-dismiss
  });
  shadow.appendChild(overlay);

  document.body.appendChild(host);
  return { host, overlay };
}

function button(label, onClick) {
  const b = document.createElement("button");
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

function el(tag, className, text) {
  const e = document.createElement(tag);
  if (className) e.className = className;
  if (text !== undefined) e.textContent = text;
  return e;
}

// Builds the modal's static chrome (header/close button, action toolbar,
// body containers) once per task, and returns an `update({status, log})`
// function to refresh its contents on every poll — cheaper and less
// flickery than rebuilding the DOM from scratch each time.
//
// `actions` wires the toolbar to renderBar's daemon calls: { onRefine,
// onImplement, onStop }. The toolbar itself decides what's clickable —
// Refine/Implement are disabled and Stop is hidden whenever the task is
// queued or running, so a task in flight can't be re-triggered and Stop
// never shows up with nothing to stop.
function buildModal(overlay, taskNumber, actions) {
  overlay.textContent = "";
  const modal = el("div", "modal");
  overlay.appendChild(modal);

  const header = el("div", "modal-header");
  header.appendChild(el("span", "modal-title", `Task #${taskNumber}`));
  const badge = el("span", "badge");
  header.appendChild(badge);
  const closeBtn = el("button", "icon-btn", "✕");
  closeBtn.title = "Dismiss";
  closeBtn.addEventListener("click", () => overlay.classList.remove("open"));
  header.appendChild(closeBtn);
  modal.appendChild(header);

  const body = el("div", "modal-body");
  const sectionsContainer = el("div");
  body.appendChild(sectionsContainer);

  // The action toolbar lives right above the agent output, since that's
  // where its effects (a fresh run, a stopped run) show up.
  const toolbar = el("div", "toolbar");
  const refineBtn = button("Refine", () => actions.onRefine());
  const implementBtn = button("Implement", () => actions.onImplement());
  const stopBtn = button("Stop", () => actions.onStop());
  stopBtn.classList.add("stop");
  const hint = el("span", "hint", "Task in progress…");
  toolbar.appendChild(refineBtn);
  toolbar.appendChild(implementBtn);
  toolbar.appendChild(hint);
  toolbar.appendChild(el("span", "spacer"));
  toolbar.appendChild(stopBtn);
  body.appendChild(toolbar);

  const streamContainer = el("div");
  body.appendChild(streamContainer);
  modal.appendChild(body);

  function renderSections(text) {
    sectionsContainer.textContent = "";
    const sections = window.__taskmanPanel.splitAnswerSections(text);
    if (sections.length === 0) return;
    for (const { title, body: sectionBody, kind } of sections) {
      const card = el("div", `section kind-${kind}`);
      card.appendChild(el("h3", null, title));
      renderMarkdownLite(card, sectionBody);
      sectionsContainer.appendChild(card);
    }
  }

  function renderMarkdownLite(container, text) {
    let ul = null;
    for (const raw of text.split("\n")) {
      const line = raw.trim();
      if (line === "") {
        ul = null;
        continue;
      }
      const bullet = line.match(/^[-*]\s+(.*)$/);
      if (bullet) {
        if (!ul) {
          ul = el("ul");
          container.appendChild(ul);
        }
        ul.appendChild(el("li", null, bullet[1]));
      } else {
        ul = null;
        container.appendChild(el("p", null, line));
      }
    }
  }

  function renderStream(logText) {
    streamContainer.textContent = "";
    const details = el("details");
    details.open = true;
    const summary = el("summary", "stream-toggle", "Agent activity");
    details.appendChild(summary);
    const stream = el("div", "stream");
    for (const line of logText.split("\n")) {
      if (line.trim() === "") continue;
      stream.appendChild(el("div", window.__taskmanPanel.classifyLogLine(line), line));
    }
    if (!logText.trim()) {
      stream.appendChild(el("div", "empty-note", "(no output yet)"));
    }
    details.appendChild(stream);
    streamContainer.appendChild(details);
    stream.scrollTop = stream.scrollHeight;
  }

  function update({ status, log, error }) {
    badge.className = `badge badge-${status}`;
    badge.textContent = STATUS_LABELS[status] || status;

    const inFlight = IN_FLIGHT_STATUSES.has(status);
    refineBtn.disabled = inFlight;
    implementBtn.disabled = inFlight;
    hint.style.display = inFlight ? "" : "none";
    stopBtn.style.display = inFlight ? "" : "none";

    if (error) {
      sectionsContainer.textContent = "";
      const card = el("div", "section kind-other");
      card.appendChild(el("h3", null, "Error"));
      card.appendChild(el("p", null, error));
      sectionsContainer.appendChild(card);
    } else if (status === "done" || status === "failed" || status === "interrupted") {
      const answer = window.__taskmanPanel.extractFinalAnswer(log || "");
      if (answer) {
        renderSections(answer);
      } else {
        sectionsContainer.textContent = "";
      }
    } else {
      sectionsContainer.textContent = "";
    }

    renderStream(log || "");
  }

  // No task run known yet: nothing in flight, so Stop has nothing to do.
  hint.style.display = "none";
  stopBtn.style.display = "none";

  return { modal, update };
}

// Renders the panel for one scraped task snapshot. Whatever this attaches
// (interval timers, etc.) is torn down by the caller removing `host`.
async function renderBar(scraped) {
  const { host, overlay } = buildBarHost();
  const mapping = await getMapping(scraped.projectName);

  if (!mapping) {
    // Nothing to run against — the systray icon just jumps to setup.
    injectSystrayToggle(() => sendToBackground({ type: "openOptionsPage", projectName: scraped.projectName }));
    return host;
  }

  let polling = null;
  let currentStatus = null; // last known status, so a re-trigger can be blocked before the daemon round-trip

  function stopPolling() {
    if (polling) clearInterval(polling);
    polling = null;
  }

  function updateModal(state) {
    currentStatus = state.status;
    modalApi.update(state);
  }

  function showModal(state) {
    overlay.classList.add("open");
    updateModal(state);
  }

  async function pollOnce() {
    const res = await sendToBackground({ type: "taskOutput", number: scraped.taskNumber });
    if (!res.ok) {
      updateModal({ status: "failed", log: "", error: res.body?.error || "could not fetch task output" });
      return;
    }
    updateModal({ status: res.body.status, log: res.body.log });
    if (!IN_FLIGHT_STATUSES.has(res.body.status)) {
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

  function runTask(kind, messageType) {
    return async () => {
      // Belt-and-suspenders: the toolbar already disables these buttons
      // while a task is in flight, but guard the handler itself too, in
      // case a click queued up right before the last poll disabled it.
      if (IN_FLIGHT_STATUSES.has(currentStatus)) return;
      try {
        const repoName = await ensureRepoFetched();
        const res = await sendToBackground({
          type: messageType,
          number: scraped.taskNumber,
          repoName,
          host: location.origin,
        });
        if (!res.ok) {
          showModal({ status: "failed", log: "", error: res.body?.error || `${kind} failed to start` });
        } else {
          showModal({ status: "queued", log: "" });
          startPolling();
        }
      } catch (err) {
        showModal({ status: "failed", log: "", error: err.message });
      }
    };
  }

  const modalApi = buildModal(overlay, scraped.taskNumber, {
    onRefine: runTask("refine", "refineTask"),
    onImplement: runTask("implement", "implementTask"),
    onStop: async () => {
      const res = await sendToBackground({ type: "interruptTask", number: scraped.taskNumber });
      if (!res.ok) updateModal({ status: currentStatus, log: "", error: res.body?.error || "interrupt failed" });
    },
  });

  injectSystrayToggle(() => {
    if (overlay.classList.contains("open")) {
      overlay.classList.remove("open");
      stopPolling();
    } else {
      overlay.classList.add("open");
      startPolling();
    }
  });

  return host;
}

// --- Watcher: keeps the bar in sync with whatever task (if any) Owl has
// currently rendered, for as long as this tab lives. ---

let currentHost = null;
let currentTaskNumber = null;
let rendering = false;

async function sync() {
  if (rendering) return; // avoid overlapping renders if mutations fire in a burst
  const scraped = window.__taskmanScrape();

  if (!scraped) {
    if (currentHost) {
      currentHost.remove();
      currentHost = null;
      currentTaskNumber = null;
    }
    return;
  }

  if (scraped.taskNumber === currentTaskNumber) return; // already showing this task

  rendering = true;
  try {
    if (currentHost) currentHost.remove();
    currentTaskNumber = scraped.taskNumber;
    currentHost = await renderBar(scraped);
  } catch (err) {
    console.error("[taskman] failed to render button bar:", err);
  } finally {
    rendering = false;
  }
}

let debounceTimer = null;
function scheduleSync() {
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(sync, OBSERVER_DEBOUNCE_MS);
}

new MutationObserver(scheduleSync).observe(document.body, { childList: true, subtree: true });
sync().catch((err) => console.error("[taskman] content script error:", err));

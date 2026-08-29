const statusEl = document.getElementById("status");

function setStatus(text) {
  statusEl.textContent = text;
}

async function bg(msg) {
  return chrome.runtime.sendMessage(msg);
}

async function loadConfig() {
  const res = await bg({ type: "getConfig" });
  if (!res.ok) {
    setStatus(`Could not load config from taskmand: ${res.body?.error || "unknown error"}`);
    return;
  }
  const harnessSel = document.getElementById("harness");
  const modelSel = document.getElementById("model");

  harnessSel.innerHTML = "";
  for (const h of res.body.harness_list) {
    const opt = document.createElement("option");
    opt.value = h;
    opt.textContent = h;
    if (h === res.body.harness) opt.selected = true;
    harnessSel.appendChild(opt);
  }

  modelSel.innerHTML = "";
  for (const m of res.body.model_list) {
    const opt = document.createElement("option");
    opt.value = m;
    opt.textContent = m;
    if (m === res.body.model) opt.selected = true;
    modelSel.appendChild(opt);
  }

  harnessSel.onchange = async () => {
    const r = await bg({ type: "setHarness", harness: harnessSel.value });
    if (!r.ok) { setStatus(`Failed to set harness: ${r.body?.error}`); return; }
    setStatus(`Harness set to ${harnessSel.value}`);
    loadConfig(); // model list depends on harness, refresh
  };
  modelSel.onchange = async () => {
    const r = await bg({ type: "setModel", model: modelSel.value });
    setStatus(r.ok ? `Model set to ${modelSel.value}` : `Failed to set model: ${r.body?.error}`);
  };
}

async function loadMapping() {
  const store = await chrome.storage.local.get(["projectMap"]);
  const map = store.projectMap || {};
  const tbody = document.querySelector("#mappingTable tbody");
  tbody.innerHTML = "";
  for (const [project, cfg] of Object.entries(map)) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${project}</td><td>${cfg.repoUrl}</td><td>${cfg.odooVersion}</td>`;
    const td = document.createElement("td");
    const del = document.createElement("button");
    del.textContent = "Remove";
    del.onclick = async () => {
      delete map[project];
      await chrome.storage.local.set({ projectMap: map });
      loadMapping();
    };
    td.appendChild(del);
    tr.appendChild(td);
    tbody.appendChild(tr);
  }
}

// If "Configure…" was clicked from a ticket page whose project isn't
// mapped yet, background.js stashed that project name for us — prefill the
// new-row form with it so the user doesn't have to retype it (and can't
// typo it, since it must match the breadcrumb exactly).
async function prefillPendingProject() {
  const store = await chrome.storage.local.get(["taskmanPendingProject"]);
  if (!store.taskmanPendingProject) return;
  document.getElementById("newProject").value = store.taskmanPendingProject;
  document.getElementById("newUrl").focus();
  await chrome.storage.local.remove("taskmanPendingProject");
}

document.getElementById("addRow").addEventListener("click", async () => {
  const project = document.getElementById("newProject").value.trim();
  const repoUrl = document.getElementById("newUrl").value.trim();
  const odooVersion = document.getElementById("newVersion").value.trim();
  if (!project || !repoUrl || !odooVersion) {
    setStatus("All three fields are required.");
    return;
  }
  const store = await chrome.storage.local.get(["projectMap"]);
  const map = store.projectMap || {};
  map[project] = { repoUrl, odooVersion };
  await chrome.storage.local.set({ projectMap: map });
  document.getElementById("newProject").value = "";
  document.getElementById("newUrl").value = "";
  document.getElementById("newVersion").value = "";
  setStatus(`Mapped "${project}".`);
  loadMapping();
});

loadConfig();
loadMapping();
prefillPendingProject();

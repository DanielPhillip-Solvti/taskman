// Toolbar popup: the "is taskmand even running" bootstrap check. Kept
// separate from the in-page modal (content.js) because it has to work
// before any Odoo task page — or even a mapped project — exists.

const dot = document.getElementById("dot");
const status = document.getElementById("status");
const downBox = document.getElementById("down-box");
const upBox = document.getElementById("up-box");

async function refresh() {
  const res = await chrome.runtime.sendMessage({ type: "checkHealth" });
  const reachable = !!res.ok;

  dot.className = `dot ${reachable ? "ok" : "down"}`;
  status.textContent = reachable ? "taskmand is running" : "taskmand not found";
  downBox.classList.toggle("open", !reachable);
  upBox.classList.toggle("open", reachable);
}

document.getElementById("copy-btn").addEventListener("click", async (e) => {
  const cmd = document.getElementById("install-cmd").textContent;
  await navigator.clipboard.writeText(cmd);
  const btn = e.currentTarget;
  const original = btn.textContent;
  btn.textContent = "Copied ✓";
  btn.classList.add("copied");
  setTimeout(() => {
    btn.textContent = original;
    btn.classList.remove("copied");
  }, 1500);
});

document.getElementById("options-link").addEventListener("click", (e) => {
  e.preventDefault();
  chrome.runtime.openOptionsPage();
});

refresh();

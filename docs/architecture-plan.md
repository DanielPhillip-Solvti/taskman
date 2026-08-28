# Taskman — Agent Orchestration for Odoo Development Workflows

A plan for a system that lets Daniel trigger, steer, and monitor CLI coding agents (Claude Code, opencode) against local Odoo/Enterprise/client-addon repos, driven entirely from buttons injected into a separate, client-facing ticketing Odoo instance.

Status: architecture agreed via interview (2026-08-28). Not yet built. This doc is the shared reference for implementation.

---

## 1. Purpose

Daniel works across a fixed set of client Odoo addon repos, all built on read-only Odoo core + Enterprise checkouts, versioned by folder nesting (e.g. `repos/17.0/odoo`, `repos/17.0/enterprise`, `repos/17.0/client-a`). Support/dev work is tracked as tickets in a separate, client-facing Odoo instance he doesn't control the schema of. The goal is to drive a ticket's lifecycle — refine, get PM approval, implement, monitor, complete — by clicking buttons on the ticket page, while the actual coding work happens via CLI agents running against the real repos on his own machine, safely separated from the raw power (Docker socket, git credentials) that work requires.

This is a **single-operator, single-machine** tool. No multi-user or team-sync requirements for v1.

---

## 2. Component Overview

Four pieces, deliberately kept as decoupled as possible:

1. **Browser extension** — injected into the ticketing Odoo's pages. Dumb by design: scrapes project name, task number, and description off the page (via URL pattern + DOM), renders state-conditional buttons, writes back into the chatter/description via DOM injection, and talks to the local daemon over HTTP/WebSocket on `localhost`. Holds the project→repo→Odoo-version mapping in `chrome.storage.local`.
2. **Local daemon** — the only component with real host privileges (Docker socket, git credentials). Runs on Daniel's machine, exposes a constrained HTTP/WS API to the extension and to agent sessions. Owns all task state (the six-stage workflow), the Docker-lifecycle verb set, and git/PR operations. Never gives the sandbox a raw credential or the raw Docker socket.
3. **Sandbox / agent execution environment** — where Claude Code / opencode actually run, with the repos directory mounted. Each task is launched **from that task's specific repo directory** (not a shared root), so concurrent tasks against different repos never collide on cwd. Agents call back into the daemon's constrained API for anything privileged (deploy, git, PR, headless-browser screenshot capture).
4. **Local Odoo test environments** — disposable per-repo/per-version Odoo containers, managed exclusively through the daemon's Docker verbs, used for actually running and debugging the code an agent is changing.

```
[Ticketing Odoo, remote]
        |  (DOM scrape / inject via content script)
[Browser extension] --HTTP/WS(localhost)--> [Local daemon] --docker API / git creds--> [Docker engine, host]
                                                   |                                          |
                                                   | invokes                                  | manages
                                                   v                                          v
                                          [Agent CLI process,                      [Local Odoo test containers,
                                           cwd = task's repo dir]                   per repo/version]
                                                   |
                                                   | reads/writes
                                                   v
                                          [repos/<version>/<repo>/.context/]
```

---

## 3. Repository & Context Layout

Existing physical structure (already true today, not something to build): repos are nested by Odoo version —

```
repos/
  17.0/
    odoo/            (read-only core, git pull only)
    enterprise/       (read-only, git pull only)
    client-a/         (read-write addons repo)
      .context/
        odoo -> ../../odoo/.context          (relative symlink)
        enterprise -> ../../enterprise/.context (relative symlink)
        knowledge.md                          (client-a-specific notes, conventions, gotchas)
        tasks/
          4821/                               (per-ticket working state, see §5)
    client-b/
      .context/ ...
  16.0/
    odoo/
    enterprise/
    client-c/
```

- **Global knowledge** (Odoo core / Enterprise behavior, version-specific) lives once per version, at `repos/<version>/odoo/.context` and `.../enterprise/.context`, and is reached from every client repo at that version via a **fixed relative-path symlink** — no explicit version metadata needed, since nesting already encodes it.
- **Client-specific knowledge** lives in that repo's own `.context/`, curated over time (manually and/or by agents appending findings).
- **Cross-harness sharing, minimal glue**: each harness's native project-config file (Claude Code's `CLAUDE.md`, opencode's equivalent) is a symlink (or one-line stub) pointing at the same canonical `.context/knowledge.md`. Shared skills (e.g. the headless-browser screenshot capability, see §6) live once under `.context/skills/`, with each harness's own skill-discovery folder symlinked to it. No runtime translation layer between harnesses — just static symlinks set up once per repo onboarding.
- **Agent execution always launches from the task's repo directory** (`repos/<version>/<repo>/`), never from a shared root. This gives free concurrency (two tasks on two different repos never share a cwd) at the cost of no shared *live* working directory — which is fine, since the daemon holds cross-task state, not the filesystem.
- **Cross-client isolation**: explicitly not required. All repos may be mounted into the same sandbox; no technical separation between clients' code.

---

## 4. The Daemon's Constrained API

The daemon is the one piece allowed to touch the Docker socket and git credentials directly. Its verb surface should stay small and enumerable — not a proxy for arbitrary Docker/git commands:

**Docker / Odoo test-env lifecycle**
- `deploy(repo, changed_modules)` — composite operation: `git pull` on `odoo` + `enterprise` for that version → ensure the repo's Odoo container is running (start if not) → kill the running `odoo` process inside the container → restart with `-u <changed_modules>`.
- `status(repo)`, `logs(repo, tail=N)`, `stop(repo)`, `restart(repo)` — standard lifecycle/inspection verbs.
- `odoo_shell(repo, script)` — `docker exec` into the running instance's `odoo-bin shell`. (Local test data is [confirm: synthetic vs. real — see §9 open items]; if always synthetic, this needs no extra gating beyond "which container." If real/anonymized client data can appear here, revisit whether this needs audit logging like the git verbs below.)

**Git / PR (no raw credentials ever enter the sandbox)**
- `pull(repo)` — update a read-write client repo.
- `push_branch(repo, branch)`
- `open_pr(repo, branch, title, body)`
- All three are logged with timestamp + task ID, giving a natural audit trail of every push/PR the system has made.

**Agent process management**
- `start_task(repo, task_id, prompt)` — launches the chosen harness's CLI from `repos/<version>/<repo>/`, with `task_id` used to key session/state and locate `.context/tasks/<task_id>/`.
- `send_message(task_id, text)` — turn-based resume: re-invokes the harness's own resume/continue mechanism with the new message appended to history (not live stdin injection).
- `stop(task_id)` — force-kills the current run so a new steering message can be sent without waiting for natural completion.
- `get_state(task_id)` — returns the task's current workflow stage, used by the extension to decide which buttons to render.

No hard timeout or token budget in v1 — human supervision (Daniel watching Monitor) is the safety net. A per-task token limit is a plausible cheap add later if runs start getting expensive, but isn't worth engineering now.

---

## 5. Ticket Lifecycle / State Machine

State lives **entirely in the daemon**, keyed by `(project, task_number)` as scraped by the extension — never as a custom field on the ticketing Odoo, since Daniel doesn't control that schema. The extension's only job re: state is to ask the daemon "what state is this task in" and render buttons accordingly.

| Button | What it does |
|---|---|
| **Refine Ticket** | Agent investigates: compares the request against the current codebase, drafts acceptance criteria, surfaces open questions. Runs as a task in the sandbox; may take a while (watchable via Monitor). |
| *(automatic, no button)* | Once refinement finishes, the polished description is **appended** (never overwrites the original requester's text) to the ticket's description field. Applied lazily — the extension patches it in the next time the ticket page is viewed, since it only runs as a content script on page load. |
| **Request Refinement** | Manually triggered. Populates a draft chatter message ("description updated, some questions were raised, please confirm") for Daniel to review and send via Odoo's own compose UI. Purely a communication step, decoupled from the description update above. |
| **Request Approval** | Same drafting mechanism as Request Refinement, addressed to the PM instead of the reporter. **No automated approval detection** — Daniel reads the PM's chatter reply himself and decides when to proceed. This is an honor-system gate, not a technical lock. |
| **Implement** | Daemon runs `deploy()` then launches/resumes the agent to make the actual code change, iterating with `-u module_name` reloads as needed. |
| **Monitor** | Turn-based chat view onto the running/paused agent session (`send_message`/`stop` per §4) — reads like a terminal session transcript, rendered in the extension's panel. |
| **Complete** | Agent uses a headless-browser skill (§6) to capture evidence screenshots against the local test instance it just deployed to, then drafts a summary + attachments into the chatter for Daniel to review and send. |

---

## 6. Screenshot / Evidence Capture

The agent has a dedicated **skill** (a packaged, harness-visible capability, shared via the symlink mechanism in §3) that operates a headless browser against the *local test Odoo instance* (not the remote ticketing Odoo) to capture evidence of the fix — reusing whatever browser-automation capability is likely already needed to reproduce the bug during Implement.

---

## 7. Security Model — Summary

- **No raw Docker socket in the sandbox.** All container lifecycle goes through the daemon's small enumerable verb set (§4). Blast radius of a hallucinated/wrong agent command is limited to what those verbs allow, not full host root.
- **No raw git/PR credentials in the sandbox.** Same pattern — the agent asks the daemon to `pull`/`push_branch`/`open_pr`; the daemon holds the actual token and logs every operation.
- **Cross-client isolation: explicitly not required.** All repos may be visible to any sandbox session.
- **Approval gates are honor-system, not enforced.** Both Request Refinement and Request Approval are "populate a draft, human reviews and sends" — the software never blocks Implement on detecting an actual approval reply.
- **No hard runaway-agent protection in v1** — relies on Daniel watching Monitor. A token/cost budget is a candidate v2 addition if this proves risky in practice.

---

## 8. Harness Support

v1 must support **both Claude Code and opencode** via CLI (not just one), invoked with minimal glue:

- The daemon's `start_task`/`send_message`/`stop` verbs (§4) should be written against a small internal adapter seam even though only two harnesses exist today, since a third (a ChatGPT-style CLI) was mentioned as a future possibility.
- Cross-harness memory/context/skill sharing is achieved entirely through the static-symlink approach in §3 — no runtime translation code. Each harness reads its own native config location, which happens to point at the same underlying files.

---

## 9. Open Items / Needs a Decision or Investigation Before Build

These came up during the interview but weren't fully resolved — flagging them explicitly rather than silently assuming:

- **Does the local Odoo test env ever load real (even anonymized) client production data**, or is it always synthetic/demo? This affects whether `odoo_shell()` needs any extra audit/gating beyond normal access logging.
- **Git hosting provider** (GitHub/GitLab/self-hosted?) and whether PR creation needs provider-specific handling per client.
- **Exact URL/DOM scraping approach** for the ticketing Odoo instance — depends on its specific version (URL scheme differs between older `#model=...` hash routing and newer Odoo 17+ path-based routing) and whatever custom theme/view it uses for tasks.
- **Project→repo→version mapping maintenance** — confirmed as a manually-maintained `chrome.storage.local` entry per project; worth a small options-page UI in the extension so this isn't raw JSON editing.
- **Multi-harness adapter shape** — not designed in detail yet; first real implementation work should validate the adapter interface against both Claude Code's and opencode's actual resume/session semantics before assuming they're symmetric.

---

## 10. Suggested Build Sequence

1. **Daemon skeleton**: Docker-lifecycle verbs (`deploy`, `status`, `logs`, `stop`, `restart`) against one known repo, no agent invocation yet. Validate the constrained-surface approach works for real deploy cycles.
2. **Agent invocation**: `start_task`/`send_message`/`stop` against one harness first (whichever Daniel uses day-to-day), launched from the repo directory, with `.context` symlink layout in place for one repo.
3. **Second harness adapter**: add the other CLI tool, confirming the adapter seam actually holds up against two genuinely different tools.
4. **Git/PR verbs**: `pull`/`push_branch`/`open_pr`, with audit logging.
5. **Browser extension v0**: scrape project/task/description off one real ticket page, render static buttons, call daemon's `get_state`. No writing back yet.
6. **Write-back**: description append-on-view, draft-message population for Request Refinement/Request Approval/Complete.
7. **Monitor panel**: turn-based thread view + stop/resteer.
8. **Headless-browser skill**: screenshot capture for Complete, shared via the symlink mechanism so both harnesses can use it.
9. **Multi-repo/multi-client rollout**: add remaining clients' project→repo mappings, confirm concurrency actually works cleanly across two simultaneous tasks on different repos.

---

## Appendix: Decisions Log

Quick-reference of what was decided and why, from the interview that produced this plan:

- **Trigger model**: human-initiated, record-scoped (buttons on tickets), not event-driven/autonomous.
- **Two Odoo instances**: ticketing/PM Odoo (remote, schema not controlled) is separate from local disposable test Odoo envs.
- **Directory model**: agent always launches from the task's actual repo directory; concurrency falls out for free since different tasks use different cwds.
- **Global knowledge**: delivered via relative-path symlinks reflecting existing version-folder nesting, not explicit metadata.
- **Docker access**: constrained daemon API, not raw socket.
- **Cross-client isolation**: not needed.
- **Git/PR credentials**: mediated by the daemon, never present in the sandbox.
- **Extension design**: dumb — DOM scrape/inject only, no Odoo API calls, holds only the project→repo mapping locally.
- **State ownership**: daemon, not the ticketing Odoo.
- **Monitor interaction model**: turn-based resume (not live stdin injection), plus explicit stop-and-resteer.
- **Description updates**: automatic, append-only, applied on next view.
- **Request Refinement / Request Approval**: both are draft-message-population actions; approval is honor-system, not enforced.
- **Screenshots**: agent-captured via a dedicated headless-browser skill.
- **Runaway-agent protection**: none in v1; human supervision only.
- **Harnesses**: Claude Code and opencode both required in v1, minimal-glue adapter, shared context/skills via static symlinks.
- **Scale**: single machine, single operator, `chrome.storage.local`.

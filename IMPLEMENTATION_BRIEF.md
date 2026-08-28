# Taskman — Implementation Brief

**Purpose:** this document is the standing instruction set for the agent(s) building Taskman. Drop it in the new repo as `IMPLEMENTATION_BRIEF.md`, symlink `CLAUDE.md` / `AGENTS.md` to it, and use §1 as the first-turn prompt.

**Reads with:** `architecture-plan.md` (rationale + decision log) and `technical-spec.md` (the contract). Where this brief and the spec disagree, the spec wins; if the spec is wrong, fix the spec in the same commit rather than diverging silently.

---

## 1. Kickoff Prompt

> You are implementing **Taskman**, a system that orchestrates CLI coding agents against local Odoo repositories, driven from buttons injected into a remote ticketing Odoo instance. The full contract is in `technical-spec.md`; the reasoning behind it is in `architecture-plan.md`. Read both before writing code.
>
> You are working **autonomously and self-correcting**. That means: you do not ask for approval between steps, you prove each milestone with an executable gate rather than by asserting it works, and when a gate fails you diagnose and fix rather than moving on. Your work is bounded by three rules that override everything else:
>
> 1. **Never touch anything outside the project directory and the `taskman-test-*` Docker namespace.** No real client repos, no existing containers, no `docker system prune`, no global config changes.
> 2. **A milestone is not done until `scripts/verify_M<n>.sh` exits 0 on a clean run.** Not "should work", not "works apart from". Zero.
> 3. **Circuit breaker: if the same gate fails three times with the same root cause, stop.** Write the failure, what you tried, and what you believe is blocking into `BLOCKERS.md`, then move to the next independent milestone or halt. Do not thrash.
>
> Start with **Milestone 0** (§6): scaffold the repo, then run the three spikes in §5 and write your findings into `docs/spikes/`. Those three unknowns shape interfaces you will otherwise have to rewrite. Do not begin Milestone 1 until they are answered.
>
> Work in small commits with real messages. Keep `docs/DECISIONS.md` updated whenever you resolve an ambiguity in the spec — that file is how the human reviewer catches you diverging from intent.

---

## 2. Stack

Chosen for a system that is mostly glue around Docker, HTTP and process supervision, and that a solo Odoo developer must be able to debug at 11pm without a context switch.

### Daemon (`taskmand`)

| Concern | Choice | Why |
|---|---|---|
| Language | Python 3.12 | Matches the Odoo ecosystem; the daemon parses Odoo logs and drives `odoo-bin`, so being in the same language as the thing it supervises pays off constantly |
| Packaging | `uv` + `pyproject.toml` | Fast, reproducible, single lockfile; avoids venv ceremony in the agent's inner loop |
| HTTP/WS | FastAPI + uvicorn | One process serves the extension API, the WebSocket stream, and the MCP endpoint |
| MCP | official `mcp` Python SDK, streamable HTTP transport | Don't hand-roll the protocol |
| Docker | `docker` SDK for container/exec/logs; `subprocess` for `docker compose` | The SDK's compose support is weak; compose is better driven as a subprocess with parsed output |
| State | `sqlite3` stdlib + hand-written SQL + a 20-line migration runner | The schema is nine columns wide. An ORM is more failure surface than it saves; raw SQL keeps the agent's mental model and the reviewer's identical |
| Models | `pydantic` v2 | Tool schemas, API payloads and `task_report` validation all want the same thing |
| Config | `pydantic-settings` + YAML | `~/.taskman/config.yaml`, `taskman.yaml` per repo |
| Lint/type | `ruff` + `mypy --strict` | Non-negotiable, see §4 |
| Test | `pytest`, `pytest-asyncio`, `respx` | Two tiers, see §4.2 |

**Explicitly rejected:** Celery/Redis (a task queue for a single-operator serial workflow is ceremony); an ORM; a frontend framework in the daemon; Docker-in-Docker.

### Extension

| Concern | Choice | Why |
|---|---|---|
| Language | TypeScript, strict | The daemon's Pydantic models are the source of truth; generate TS types from the OpenAPI schema so the two cannot drift |
| Build | Vite + `@crxjs/vite-plugin` | MV3 handled, HMR during development |
| Framework | **None.** Vanilla DOM for injection, and either vanilla or Preact for the Monitor panel | The content script must coexist inside Odoo's Owl-rendered DOM; a framework fighting Owl for the same nodes is a bad trade. The Monitor panel is a log view and a text box |
| Styling | Scoped CSS with a `taskman-` prefix, shadow DOM for the injected bar | Odoo's stylesheet is aggressive; isolate |

### Evidence capture

Playwright (Python) inside the sandbox image. Deterministic viewport, step-list driven — never agent-authored browser code (spec §9).

---

## 3. Repository Layout

```
taskman/
  pyproject.toml            uv.lock
  IMPLEMENTATION_BRIEF.md   CLAUDE.md -> IMPLEMENTATION_BRIEF.md
  BLOCKERS.md
  docs/
    architecture-plan.md  technical-spec.md
    DECISIONS.md          spikes/
  daemon/
    __main__.py           app.py            config.py
    state/                store.py  models.py  migrations/
    docker/               engine.py  compose.py  odoo_env.py
    odoo/                 deploy.py  logparse.py  shell.py
    git/                  local.py  remote.py  forge.py
    mcp/                  server.py  tools.py  auth.py
    harness/              base.py  claude.py  opencode.py  events.py
    tasks/                lifecycle.py  phases.py  reports.py
    locks.py  audit.py
  sandbox/
    Dockerfile            skills/odoo-evidence/{SKILL.md,capture.py}
  extension/
    src/{content,worker,panel,options}/    manifest.json
  fixtures/
    repos/17.0/...        odoo-logs/       ticket-pages/
  scripts/
    verify_M0.sh ... verify_M10.sh
    bootstrap_fixtures.sh
  tests/
    unit/  integration/
```

---

## 4. Development Approach

The whole point of this section is that **autonomy requires observable, executable truth**. An agent cannot self-correct against a subjective standard. Everything below exists to convert "does it work?" into an exit code.

### 4.1 Executable milestone gates

Every milestone in §6 has `scripts/verify_M<n>.sh`. A gate:

- sets up its own state from scratch (fresh temp dir, fresh `taskman-test-*` containers),
- exercises the milestone's capability **through its real interface**, not by calling internals,
- tears down,
- exits 0 or 1 with a readable reason.

Gates are cumulative: `verify_M5.sh` runs M1–M4 first. This is what catches regressions during autonomous work, where nobody is watching.

Write the gate **before** the implementation. It is the specification made executable, and it is the feedback signal the self-correcting loop runs on.

### 4.2 Two test tiers

| Tier | Speed | Docker? | Run when |
|---|---|---|---|
| **Unit** | < 5s total | No — Docker layer behind a `DockerEngine` protocol with a fake | Every edit |
| **Integration** | 1–10 min | Yes, real containers, real Odoo | Milestone gates, pre-commit on daemon changes |

The `DockerEngine` protocol seam is worth building on day one specifically so the agent's inner loop stays fast. An agent that must wait 90s for an Odoo boot to learn it made a typo will burn enormous effort.

### 4.3 Golden-file testing for the log parser

**This is the highest-leverage test in the project.** Spec §6.2 step 8 — turning Odoo startup output into `{level, module, message, traceback}` — is the piece every implement-iteration depends on, and it is pure text-in/struct-out, so it can be tested exhaustively without Docker.

Capture real Odoo startup logs into `fixtures/odoo-logs/` covering at minimum: clean success; XML `ParseError`; Python `ImportError` in a module; missing dependency in `__manifest__.py`; `psycopg2` constraint violation during upgrade; a module that loads with warnings; a hang (truncated log). Each gets an expected-parse JSON alongside it. Add a fixture every time the parser surprises you in real use — that is the mechanism by which this component gets good.

### 4.4 Fixture repository

The building agent must never test against real client repos. `scripts/bootstrap_fixtures.sh` creates:

```
fixtures/repos/17.0/
  odoo/         ← shallow clone (--depth 1 --branch 17.0), cached
  enterprise/   ← stub: a directory with a couple of fake module dirs
                   (the real one is private; nothing under test needs its content)
  demo_client/  ← a real, minimal Odoo addon: one model, one view, one test,
                   plus a deliberately broken variant on a branch for failure-path testing
```

`demo_client` having a **deliberately broken branch** matters — half the gates need to prove failure paths report correctly, and a real `ParseError` from a real Odoo boot is worth more than a mocked one.

### 4.5 Contract-first, generated types

Order of work within any feature that crosses a boundary:

1. Pydantic model / MCP tool schema
2. Gate that exercises it
3. Implementation
4. `openapi.json` regenerated, TS types regenerated for the extension

The extension must never hand-write a type the daemon owns. Add a CI-style check to the gates that regeneration produces no diff.

### 4.6 Error discipline

Self-correction is impossible if failures are swallowed. Therefore:

- No bare `except:` and no `except Exception: pass`. Ruff enforces.
- Every subprocess/Docker call returns structured `{ok, stdout, stderr, exit_code, duration}` — never a bare string.
- Every failure path that a human or agent might hit produces a message naming **what was attempted, what happened, and what to check**.
- The daemon logs structured JSON to `~/.taskman/daemon.log`; the audit log (spec §4.4) is separate and append-only.

### 4.7 Working the loop

The intended inner loop, stated explicitly because it is the thing being asked for:

```
read gate → write gate → implement → run unit tests → run gate
   → fail? read the actual error, form one hypothesis, test it, fix
   → pass? commit, update DECISIONS.md if anything was ambiguous, next
   → failed 3× same cause? BLOCKERS.md, move on or halt
```

Two anti-patterns to watch for in yourself: **widening the net** (rewriting working code because a gate fails somewhere else — read the error first), and **weakening the gate** (making a test pass by asking less of it). If a gate is genuinely wrong, change it in its own commit with the reasoning, never bundled into a fix.

---

## 5. Spikes — Do These First

Three unknowns that will force interface rewrites if discovered late. Timebox each; write findings to `docs/spikes/<name>.md` including the exact versions probed.

**S1 — Harness session semantics.** For both Claude Code and opencode: how is a session identified and resumed? What does streaming output look like structurally? How does the CLI accept an MCP server config, and can it carry a bearer header? What happens on `SIGINT` mid-turn — is the session recoverable? Produce a comparison table and a proposed `Harness` protocol (spec §12) shaped by both, not one. **This is the highest-risk unknown in the project.**

**S2 — Odoo process lifecycle inside a warm container.** Prove the spec §6.1 claim: entrypoint sleeps, `odoo-bin` is killed and restarted with `-u` repeatedly. Measure a warm cycle for a one-module update. Determine the exact readiness and failure log lines for the target Odoo version. Confirm `pkill -TERM -f odoo-bin` leaves the container and Postgres healthy across ~20 cycles. If warm cycles exceed ~60s, say so loudly — the whole UX assumption depends on this being fast.

**S3 — Odoo UI injection.** Against a real ticket page: what selectors identify project name, task number, description, and the chatter composer? Does setting content plus an `input` event survive Owl's reactivity? Can `DataTransfer` attach files to the composer's hidden file input? Does an appended `<div data-taskman-block>` survive a save/reload round-trip? Produce the initial selector map (spec §10.2) as data, not code.

---

## 6. Milestones and Gates

Ordered so that risk is retired before UI is built. Steps 1–5 give a fully functional system driveable by `curl`.

| # | Deliverable | `verify_M<n>.sh` passes when |
|---|---|---|
| **0** | Scaffold, config loading, SQLite + migrations, `DockerEngine` protocol + fake, fixtures bootstrapped, spikes written | Daemon starts, `/health` responds, fixture repos exist, all three spike docs present |
| **1** | Odoo env management + **deploy pipeline** + log parser | A real `-u` cycle on `demo_client` returns `ok:true` with correct `modules_updated`; the broken branch returns `ok:false` with a correctly parsed `ParseError` naming file and line |
| **2** | MCP server, per-task tokens, Odoo tool group, sandbox image | Claude Code, inside the sandbox with no Docker socket, deploys and reads logs purely via tools; a forged token for another repo is rejected; every call appears in the audit log |
| **3** | opencode adapter, normalised `Event` stream | opencode completes the same scripted task; both produce identical normalised event shapes; stop-and-resume works on both |
| **4** | Git local + remote tools, forge integration | Branch created, commits made in-sandbox, `git_push` + `pr_open` produce a real draft PR against a scratch remote; `grep -r` for the token in the sandbox filesystem and environment finds nothing |
| **5** | Task lifecycle, phases, `task_report`, locking | Full refine→implement→complete lifecycle driven by `curl` alone; second task on the same repo is refused with the holder's id; two tasks on different repos run clean and concurrent; daemon killed mid-phase recovers to `interrupted` |
| **6** | Extension: scrape + state-conditional buttons | Against a saved fixture ticket page, correct `{project, task, title, description}` extracted and correct button set rendered for each of the nine states |
| **7** | Monitor panel | Live transcript + `task_note` breadcrumbs stream over WS; Stop halts a running phase within 20s; a re-steer message resumes the session |
| **8** | Write-back | Description append is idempotent across three page reloads and one re-refinement; chatter draft populated with text and PNG attachments; **nothing is ever sent** — assert the send button was not clicked |
| **9** | Evidence skill | `evidence_capture` produces registered PNGs from the local test instance and they arrive as chatter attachments |
| **10** | Rollout | Real repos mapped; two concurrent real tasks; audit log reviewed end-to-end |

---

## 7. Guardrails for the Building Agent

Restating the hard limits, because autonomy without these is just risk:

- **Namespace everything.** Containers `taskman-test-*`, networks `taskman-test-net`, databases `taskman_test_*`. Never `docker system prune`, never operate on a container you did not create.
- **Never touch real client repos.** All development runs against `fixtures/`. Milestone 10 is the first contact with reality and is human-driven.
- **No credentials in the repo.** Ever. Not in fixtures, not in tests, not in a comment. Tests that need a forge use a local scratch remote or a recorded fixture.
- **The spec is the contract.** Diverging is allowed, silently diverging is not: change `technical-spec.md` in the same commit and note it in `DECISIONS.md`.
- **Keep the tool surface closed.** Do not add a general-purpose escape hatch to the MCP server — no `run_command`, no socket proxy, no "just for debugging" verb. If a capability is needed, it becomes a named, scoped, audited tool.
- **When genuinely blocked, stop and say so.** A clear `BLOCKERS.md` entry is a successful outcome. Fabricating progress is not.

---

## 8. What "Done" Looks Like for v1

Daniel opens a ticket in the client Odoo. A button bar appears. He clicks **Refine Ticket**, watches the analysis in the Monitor panel, and reloads to find a clearly-labelled refined specification appended below the original description. He clicks **Request Approval**, edits the drafted message, and sends it himself. When the PM replies, he clicks **Implement**, watches the agent deploy and iterate against the local Odoo, steps in once to redirect it, and ends with a draft PR. He clicks **Complete**, reviews the screenshots and summary sitting in the chatter composer, and hits Send.

At no point did anything reach a human or a shared branch without him clicking. At no point did the sandbox hold a credential or a Docker socket. Every privileged action is one line in `audit.jsonl`.

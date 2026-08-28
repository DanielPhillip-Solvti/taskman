# Taskman — Technical Specification

**Version:** 1.1 (draft for implementation)
**Date:** 2026-08-28
**Supersedes detail in:** `architecture-plan.md` (that document remains the rationale/decision log; this one is the build spec)

---

## 0. Governing Philosophy

Three invariants drive every decision below. Where a design choice was ambiguous, it was resolved in favour of these.

**I1 — Human supervision is the safety mechanism.** No agent action reaches a client, a shared branch, or a person's inbox without Daniel explicitly clicking or sending it. The system may *prepare* anything; it may *commit* almost nothing. Concretely: chatter messages are always drafted and never auto-sent; PRs are opened as drafts; approval gates are advisory, not enforced. There is deliberately no autonomous trigger path in v1.

**I2 — The sandbox is untrusted.** The container where agents run holds no Docker socket, no git credentials, no client secrets, and no route to anything outside an explicit allowlist. It is assumed that an agent may attempt any command, correctly or by hallucination, and the blast radius must remain inside the sandbox plus whatever the tool surface deliberately exposes.

**I3 — Elevated privilege is reached only through typed tool calls.** Every operation that crosses the sandbox boundary — touching Docker, the network, a remote git host, or a real browser — is a named tool with a declared schema, an owner-side implementation, an audit log entry, and a scope check. There is no general-purpose escape hatch (no `run_host_command`, no socket proxy). Adding a capability means adding a tool, deliberately.

A useful test when in doubt: *if an agent did the worst plausible thing with this interface, what happens?* If the answer isn't bounded and loggable, the interface is wrong.

---

## 1. Topology and Trust Boundaries

```
┌─ Daniel's browser ────────────────────────────────────────┐
│  Ticketing Odoo (remote, schema not controlled)           │
│    └─ content script: scrape + inject + draft             │
│  Extension service worker ──────────┐                     │
└─────────────────────────────────────┼─────────────────────┘
                                      │ HTTP/WS, localhost, bearer token
                     ═════════════════╪═══════════ TRUST BOUNDARY ═══════
┌─ Host (Daniel's machine) ───────────▼─────────────────────┐
│  taskmand (daemon)  — the ONLY privileged component        │
│    · task state (SQLite)      · docker socket              │
│    · git credentials          · audit log                  │
│    · process supervision      · MCP server (per-task auth) │
│    · lock manager                                          │
└───────┬──────────────────────────────┬────────────────────┘
        │ docker API                   │ MCP over HTTP (host-gateway)
        │                    ══════════╪══════ TRUST BOUNDARY ══════
┌───────▼──────────────┐   ┌───────────▼───────────────────┐
│ Odoo test containers │◄──┤ Sandbox container              │
│ taskman-odoo-<repo>  │   │  · Claude Code + opencode      │
│ + postgres           │   │  · repos/ mounted              │
│ on taskman-net       │   │  · playwright + chromium       │
└──────────────────────┘   │  · NO docker sock, NO git creds│
                           │  on taskman-net                │
                           └────────────────────────────────┘
```

Two boundaries, two different threat models:

- **Extension → daemon** guards against *other web pages*. The daemon binds `127.0.0.1` only and requires a bearer token; any origin on the machine could otherwise POST to it. See §10.1.
- **Sandbox → daemon** guards against *the agent itself*. This is the boundary that matters most and is specified in §4.

---

## 2. The Sandbox

### 2.1 Image

A single long-lived image, rebuilt when tooling changes. Contents:

| Layer | Contents |
|---|---|
| Base | `ghcr.io/solvti/odoo-env:<version>` — the same base image the real `odoo-env-<version>` dev containers run (confirmed: Python 3.12.3, Debian-based), so linting and static analysis see exactly the runtime the code actually executes under, not an approximation |
| Agents | Claude Code CLI, opencode CLI |
| Odoo tooling | `pylint-odoo`, `odoo-test-helper`, `black`, `ruff`, Odoo's own `requirements.txt` deps (so imports resolve for static analysis without a running server) — none of these are preinstalled in the base `odoo-env` image today, so the sandbox image adds them as an extra layer |
| Browser | Playwright + headless Chromium (for the evidence-capture skill, §9) |
| Runtime | `git` (local operations only — see §7), `ripgrep`, `jq` |

### 2.2 Lifecycle

**One sandbox container per active task**, created on `start_task` and destroyed on task completion or abandonment. Rationale: cheaper than it sounds (image is warm, startup ~1s), and it gives clean process isolation between concurrent tasks on different repos without any in-container multiplexing. It also makes "stop and re-steer" (§8.3) trivially reliable — worst case, kill the container.

The container name is `taskman-agent-<task_id>`, which makes orphan cleanup a one-liner on daemon startup.

### 2.3 Mounts

| Host path | Container path | Mode | Notes |
|---|---|---|---|
| `odoo-env-<version>/` | `/repos/<version>/` | `rw` | Whole per-version env checkout (see §3.1 — this is the real `odoo-env-<version>` directory, e.g. `odoo-env-19/`), so relative `.context` symlinks resolve |
| — | `/tmp/taskman` | `rw` (tmpfs) | Scratch; discarded with the container |

The full env checkout is mounted rather than just the task's repo, because the `.context/odoo` and `.context/enterprise` symlinks are relative and must resolve, and because `odoo/`, `enterprise/` and `design-themes/` are siblings of `repos/` inside that checkout, not nested under it (§3.1). Odoo and Enterprise are mounted read-write at the OS level only because Docker bind-mount granularity makes read-only awkward when they share a parent; **write protection for them is enforced by tool-surface design and a pre-commit guard, not by mount flags** — see §7.4. If stricter guarantees are wanted later, mount `odoo/` and `enterprise/` as separate `ro` binds and accept the extra mount plumbing.

Working directory on launch: `/repos/<version>/repos/<repo>/`. Always. Never a shared root.

### 2.4 Network policy

The sandbox joins `taskman-net` (a user-defined bridge network) and has egress restricted to:

| Destination | Why |
|---|---|
| `api.anthropic.com`, and the API host for whichever model backs opencode | Agents cannot function without it |
| `taskman-odoo-*` containers on `taskman-net` (ports 8069, 5432) | Debugging, RPC, evidence capture |
| `host-gateway:8765` (the daemon's MCP endpoint) | Privileged tool calls |
| PyPI + npm registry (optional, see below) | Only if agents are permitted to install packages |

Everything else is denied. In particular **git hosts are not reachable from the sandbox** — this is what makes the credential boundary in §7 meaningful rather than decorative. Without it, an agent could in principle exfiltrate client code to an arbitrary remote even with no local credentials.

*Package registries:* allow them, but note that this is the weakest link in the egress policy (an agent can `pip install` arbitrary code, and PyPI is a plausible exfiltration channel). Recommended: allow, since blocking it will cause constant friction during real debugging work, and accept it as a known, documented residual risk consistent with the single-operator threat model. Revisit if the system is ever run unattended.

*Unavoidable secret:* the LLM API key must exist inside the sandbox. It is the one credential that cannot be mediated. Use a key scoped to this purpose so it can be rotated independently.

### 2.5 Resource limits

`--memory=8g --cpus=4 --pids-limit=512`. Not a security control (the boundaries above are); purely to stop a runaway test suite from wedging the host.

---

## 3. Repository and Context Layout

### 3.1 Structure

There is no single `repos/<version>/` tree today — each Odoo version is its own top-level checkout of the `Solvti/odoo-env` repo (e.g. `odoo-env-19/`, `odoo-env-18/`), pinned to that branch, each with its own `odoo/`, `enterprise/` and `design-themes/` clones as *siblings* of a `repos/` directory holding the client repos for that version. Taskman treats each `odoo-env-<version>` checkout as the "version root" referred to elsewhere in this document as `repos/<version>/`:

```
odoo-env-19/                     ← "repos/17.0/" elsewhere in this doc, generalised
  docker-compose.yml             ← defines the odoo/postgres/mailpit services (§6.1)
  odoo.conf -> repos/<active-repo>/odoo.conf   (symlink, switched by ./ln, §6.1 note)
  odoo/
    .context/                    ← Odoo-core knowledge, version-specific
  enterprise/
    .context/                    ← Enterprise-module knowledge, version-specific
  design-themes/
  repos/
    client-a/                    ← the client's own git repo (remote: git@github.com:Solvti/client-a.git)
      taskman.yaml                ← per-repo config (§3.3)
      odoo.conf, example.odoo.conf ← this repo's own conf; addons_path already lists its own subdirs (§3.3)
      addons/               ← this client's own modules (one __manifest__.py dir per module)
      external-addons/             ← vendored third-party modules
      oca-addons/                  ← OCA modules (not all repos have this one)
      .context/
        odoo -> ../../odoo/.context             (relative symlink)
        enterprise -> ../../enterprise/.context (relative symlink)
        knowledge.md       ← canonical client knowledge  [committed]
        skills/            ← shared agent skills          [committed]
        prompts/           ← optional per-repo prompt overrides [committed]
        tasks/             ← per-task working state       [gitignored]
          4821/
            transcript.jsonl
            report.json
            evidence/
      CLAUDE.md -> .context/knowledge.md
      AGENTS.md -> .context/knowledge.md
      .agents/skills -> ../.context/skills   ← canonical dir; both harnesses read from here
      .claude -> .agents                     ← matches the existing convention already used in
                                                odoo-env checkouts (see `.agents/skills/*` today)
      .gitignore           ← must contain `.context/tasks/`
```

Note the module-directory layout: a client repo's manifests live under `addons/`, `external-addons/`, and (sometimes) `oca-addons/` — never at the repo root. Every place below that scans for `__manifest__.py` to detect changed modules (§6.2) or assembles `addons_path` (§6.1) must walk those three subdirectories, not the repo root.

### 3.2 Committed vs. ephemeral — a correction to the plan

The architecture plan put per-ticket working state under `.context/tasks/`. That is kept, **but `.context/tasks/` must be gitignored** while `knowledge.md`, `skills/` and `prompts/` are committed. Rationale: the knowledge is the durable, reviewable asset that should travel with the repo and improve over time; transcripts and screenshots are per-run noise that would pollute diffs and bloat the repo. If the repo is one where committing `.context` is unwelcome (a client repo where the addition would look odd in a PR), the fallback is a `.context` symlink pointing into a Taskman-owned sibling directory — but the default is committed, because shared, versioned knowledge is most of the value.

### 3.3 Per-repo config: `taskman.yaml`

```yaml
odoo_version: "17.0"          # redundant with nesting, but explicit beats inferred
addons_path_extra: []          # extra dirs beyond the repo's own addons/, external-addons/, oca-addons/
database: client_a_dev
base_branch: main              # for diffing changed modules & PR targeting
env:
  compose_file: null           # null → generate from template (§6.1)
  http_port: 8069
  extra_services: []           # e.g. a redis the client's addons need
test:
  tags: "/module_name"         # default --test-tags scope
lint:
  enabled: true
```

### 3.4 Cross-harness sharing

Both harnesses read the same underlying files through their own native discovery, via symlinks created once at repo onboarding. This mirrors the convention already in use in the `odoo-env` checkouts today — `.agents/skills/<skill>/SKILL.md` is the canonical, committed location (see e.g. `.agents/skills/capture-screenshots/` in `odoo-env-19`), and `.claude` is a symlink to `.agents` so Claude Code's own discovery resolves the same files with no duplication:

- `CLAUDE.md` → `.context/knowledge.md`
- `AGENTS.md` → `.context/knowledge.md` *(opencode's convention; verify against the installed version at build time — see §14)*
- `.claude` → `.agents` (whole directory, not just `skills/`), following the existing repo convention
- `.agents/skills` → `../.context/skills` (canonical target both harnesses ultimately read)

No translation layer, no sync process. A skill is a directory of plain instructions plus optional scripts; both tools can read instructions, and scripts are just executables either can invoke.

---

## 4. The Privileged Tool Surface (MCP)

This is the heart of the design and where I3 is made concrete.

### 4.1 Transport and scoping

The daemon exposes an **MCP server over streamable HTTP** at `http://host-gateway:8765/mcp`. Both Claude Code and opencode support MCP servers, which is why this rather than a bespoke RPC: one implementation serves both harnesses, and the harnesses handle tool-schema presentation, retries, and result rendering natively.

**Per-task bearer tokens.** When the daemon starts a task it mints a random token bound server-side to `{task_id, repo, version, branch}`, and injects it into the sandbox's MCP config. Every tool call is resolved against that binding:

- The repo is **never a parameter** on any tool. `odoo_deploy()` deploys *this task's* repo because the token says so. An agent cannot name another client's repo, because there is no field in which to name it.
- Tokens are revoked when the task reaches a terminal state, and on daemon restart.
- Tokens are single-task, not single-session, so they survive stop-and-resteer.

This eliminates an entire class of confused-deputy bug by construction rather than by validation.

### 4.2 What is *not* a tool

Deliberately kept as ordinary in-sandbox operations, because they need no privilege and routing them through the daemon would add friction for no security gain:

- Reading and writing files under `/repos/<version>/repos/<repo>/`
- Local git: `status`, `diff`, `log`, `branch`, `checkout`, `add`, `commit`, `stash`
- Running linters, static analysis, Python
- HTTP/RPC calls to the local Odoo test instance (it is on `taskman-net`, credentials are throwaway dev credentials)
- Querying the test Postgres directly

The line is precise: **local computation and local VCS are free; anything that touches Docker, a remote, or a real browser is a tool.**

### 4.3 Tool catalogue

#### Odoo environment

```
odoo_deploy(modules?: string[], reset_db?: boolean) -> DeployResult
```
Runs the full deploy pipeline (§6.2). `modules` omitted → auto-detected from the diff. `reset_db` drops and recreates from template — expensive, requires the task to be in `implementing`, and is logged prominently.
```
DeployResult {
  ok: boolean
  duration_s: number
  modules_updated: string[]
  errors: [{level, module?, message, traceback?}]
  log_tail: string        # last 200 lines, always present
  url: string             # e.g. http://taskman-odoo-client-a:8069
}
```

```
odoo_logs(lines?: number = 200, since?: string, grep?: string) -> string
odoo_status() -> {running, healthy, uptime_s, db, modules_installed_count}
odoo_restart(update_modules?: string[]) -> DeployResult
odoo_shell(code: string, timeout_s?: number = 60) -> {stdout, stderr, ok}
```
`odoo_shell` executes Python in `odoo-bin shell` against the test database. It is unrestricted ORM access *by design* — it is the single most useful debugging affordance in Odoo work, and constraining it would gut the tool's value. It is safe because the database is disposable local test data. **This assumption must hold**: see §14, open item on production data.

#### Git — remote operations only

```
git_pull_upstream() -> {odoo: {ok, sha}, enterprise: {ok, sha}}
```
Fast-forward-only pull of the version's `odoo` and `enterprise` checkouts. Takes the version-level lock (§5.3). Refuses if either has local modifications (which would indicate a bug or an agent overstepping — surfaced loudly).

```
git_push(branch?: string, force?: false) -> {ok, remote_url, ahead_by}
```
Pushes the task's branch. `force` is accepted but the daemon rejects it unless the branch matches `task/*` — force-pushing a shared branch is never permitted.

```
pr_open(title: string, body: string, draft?: boolean = true) -> {ok, url, number}
pr_status() -> {exists, url, state, checks?}
```
PRs are **opened as drafts by default** (I1). Base branch comes from `taskman.yaml`, not from the agent. Body is passed through a template that appends the task ID and a machine-generated provenance footer noting the PR was authored by an agent under Taskman.

#### Evidence

```
evidence_capture(steps: Step[], name: string) -> {ok, files: [{path, caption}]}
```
See §9. Runs Playwright inside the sandbox against the local test instance; it is a tool rather than a free operation only so that captured artefacts are registered with the daemon for attachment to the chatter.

#### Task reporting — the structured output channel

```
task_note(text: string) -> ok
```
A breadcrumb, surfaced live in the Monitor panel. Encouraged liberally; this is what makes supervision pleasant rather than a wall of stdout.

```
task_report(
  kind: "refinement" | "implementation" | "completion",
  summary: string,                       # markdown, human-facing
  description_append?: string,           # HTML fragment, for the ticket description
  questions?: string[],                  # open questions for the reporter/PM
  acceptance_criteria?: string[],
  changed_modules?: string[],
  evidence?: string[],                   # ids from evidence_capture
  pr_url?: string
) -> ok
```

This is important and worth stating plainly: **structured reporting via a tool, not stdout parsing.** The agent hands back a typed object; the extension renders it and the human approves it. Nothing downstream ever regex-scrapes an LLM's prose. `task_report` is what advances the state machine (§8.2) — the daemon treats receipt of a valid report as the completion signal for the current phase.

### 4.4 Audit

Every tool call appends one line to `~/.taskman/audit.jsonl`:

```json
{"ts":"2026-08-28T14:22:31Z","task":"client-a#4821","tool":"git_push",
 "args":{"branch":"task/4821-invoice-rounding"},"result":"ok","duration_ms":1840}
```

Append-only, never rotated automatically (it is small), and the one artefact that answers "what has this system ever actually done to my repos."

---

## 5. Daemon Internals

### 5.1 Implementation

Python 3.12 + FastAPI (HTTP + WebSocket + the MCP endpoint in one process), `docker` SDK for Python, SQLite for state. Python because it matches the Odoo ecosystem Daniel already works in — debugging the daemon should not require a context switch into a second language.

Run as a user-level systemd unit (or launchd agent), restart-on-failure, bound to `127.0.0.1:8765`.

### 5.2 State store

SQLite at `~/.taskman/state.db`, WAL mode.

```sql
CREATE TABLE task (
  id            TEXT PRIMARY KEY,      -- "<repo>#<task_number>"
  project_name  TEXT NOT NULL,         -- as scraped, for traceability
  task_number   INTEGER NOT NULL,
  repo          TEXT NOT NULL,
  version       TEXT NOT NULL,
  branch        TEXT,
  state         TEXT NOT NULL,         -- §8.1
  title         TEXT,
  created_at    TEXT, updated_at TEXT
);

CREATE TABLE phase (                   -- one row per agent invocation
  id         INTEGER PRIMARY KEY,
  task_id    TEXT REFERENCES task(id),
  kind       TEXT,                     -- refine | implement | complete
  harness    TEXT,                     -- claude | opencode
  session_ref TEXT,                    -- harness-native session id, for resume
  status     TEXT,                     -- running | paused | done | stopped | failed
  started_at TEXT, ended_at TEXT
);

CREATE TABLE report (                  -- task_report payloads, immutable
  id INTEGER PRIMARY KEY, task_id TEXT, phase_id INTEGER,
  kind TEXT, payload JSON, created_at TEXT,
  applied_at TEXT                      -- when the human accepted it into Odoo
);

CREATE TABLE evidence (
  id TEXT PRIMARY KEY, task_id TEXT, path TEXT, caption TEXT, created_at TEXT
);
```

Transcripts are **not** in SQLite — they live as JSONL at `.context/tasks/<n>/transcript.jsonl`, streamed and tailed. Blobs in SQLite make backup and inspection worse for no gain.

### 5.3 Locking

Three lock scopes, all daemon-held, all with the same acquire/timeout/release discipline:

| Lock | Guards | Held during |
|---|---|---|
| `version:<v>` | shared `odoo/` + `enterprise/` checkouts | `git_pull_upstream` only — short |
| `repo:<repo>` | working tree, branch, Odoo container, database | the whole active task |
| `docker:global` | container create/destroy churn | brief, prevents compose races |

**Concurrency rule: one active task per repo; unlimited tasks across different repos.** This falls out of the `repo:` lock and is the honest reading of the constraint — two agents cannot share a working tree and a database without stepping on each other, and `git worktree` (the obvious alternative) was considered and rejected for v1 because it breaks the relative `.context` symlinks and doubles the Odoo-container matrix for a concurrency scenario Daniel does not currently have.

If a second task is requested on a locked repo, the extension shows *"client-a is busy with #4812"* rather than queueing silently.

### 5.4 Process supervision

Each phase is a container (`taskman-agent-<task_id>`) with the harness CLI as PID 1's child, stdout/stderr streamed to `transcript.jsonl` and fanned out to any connected Monitor WebSocket. The daemon watches for exit; unexpected exit → `phase.status = failed`, surfaced in Monitor with the last 50 lines.

On daemon restart: reconcile. Containers matching `taskman-agent-*` with no live task row are removed; tasks in `running` whose container is gone are marked `interrupted` and shown as resumable.

---

## 6. The Odoo Test Environment

### 6.1 Container topology

Per repo: one Odoo container `taskman-odoo-<repo>` + one Postgres `taskman-db-<repo>`, both on `taskman-net`. Generated from a template unless `taskman.yaml` names its own compose file.

**This is a deliberate departure from how `odoo-env` works today, worth stating explicitly.** Today, a version root like `odoo-env-19/` runs exactly one shared Odoo container (`odoo-env-19-odoo-1`) and one shared Postgres container (`odoo-env-19-postgres-1`) for *all* client repos under that version; switching which client is active is done by re-pointing the `/code/odoo.conf` symlink with `./ln repos/<repo>` and restarting the Odoo process — there is no per-repo container isolation. Taskman's one-container-per-repo model (needed for the concurrency story in §5.3) is new infrastructure Taskman must stand up alongside the existing shared containers, not a description of what already runs. The two can coexist: a developer's manual `odoo-env-19-odoo-1` is untouched; Taskman's `taskman-odoo-<repo>` containers are separate, disposable, and task-scoped.

Odoo container specifics that matter:

- **Entrypoint is a sleep, not `odoo-bin`.** The container stays up while the Odoo *process* inside it is killed and restarted repeatedly (which is precisely the deploy pipeline). Making the container's lifetime independent of the Odoo process's lifetime is what makes fast `-u` cycles possible without container churn.
- Base image: `ghcr.io/solvti/odoo-env:<version>` (the same image `odoo-env` itself builds from), so the odoo.conf format, `./start`/`./shell` helper scripts and Python version match the real dev environment exactly.
- Addons path assembled from, in order: `odoo/addons`, `enterprise`, `design-themes`, then this repo's own `addons/`, `external-addons/` and (if present) `oca-addons/`, plus `addons_path_extra` — matching the `addons_path` already written into each repo's own `odoo.conf` today.
- A `template_<db>` Postgres database is maintained for fast resets (`CREATE DATABASE x TEMPLATE template_x`), which turns `reset_db` from minutes into seconds.

### 6.2 Deploy pipeline

`odoo_deploy()` implements exactly the sequence Daniel specified, with the details filled in:

1. **Acquire `repo:<repo>`** (already held by the active task — re-entrant).
2. **Take `version:<v>`; fast-forward `odoo` and `enterprise`; release.** Abort the whole deploy if either has local modifications.
3. **Ensure the stack is up.** `docker compose up -d` if the containers aren't running; wait for Postgres readiness.
4. **Determine changed modules**, if not supplied:
   ```
   changed = git diff --name-only $(git merge-base <base_branch> HEAD)..HEAD
           ∪ git status --porcelain --untracked-files=all
   modules = { second path segment p of each changed file
               where <repo>/{addons,external-addons,oca-addons}/p/__manifest__.py exists }
   ```
   (module manifests live under those three subdirectories, never at the repo root — §3.1)
   Uncommitted files are included deliberately — the agent is mid-edit and expects its working-tree changes to be what gets loaded.
5. **If `modules` is empty**, skip `-u` and do a plain restart (a change to a non-module file, e.g. a script or config).
6. **Kill the running Odoo process**: `docker exec <c> pkill -TERM -f odoo-bin`, wait up to 15s, escalate to `-KILL`.
7. **Start with the update**: `docker exec -d <c> odoo-bin -c /etc/odoo/odoo.conf -d <db> -u <modules joined by ,>`.
8. **Watch the log for a readiness or failure verdict**, with a 300s timeout:
   - success ← `Modules loaded.` followed by the HTTP service line
   - failure ← any `CRITICAL`, a loading traceback, or a `ParseError` / `ValidationError` during registry build
   - Parse errors into structured `{level, module, message, traceback}` rather than returning a log blob — this is the single highest-value piece of glue in the system, because "the module failed to load and here is exactly why" is the answer an agent needs on nearly every iteration.
9. **Return `DeployResult`.** Always include `log_tail` even on success (warnings matter in Odoo).

Typical warm cycle for a single small module: 15–40s. This is the loop the agent will run dozens of times per task, so it is worth optimising: keep the container warm, keep the DB warm, only `-u` what changed.

---

## 7. Git and PR Workflow

### 7.1 Branching

On transition to `implementing`, the daemon (not the agent) creates and checks out `task/<number>-<slug>` from an up-to-date `base_branch`. Branch naming is daemon-owned so it is predictable and greppable.

### 7.2 What the agent does locally

Commits freely on its own branch, in the sandbox, with a configured identity (`Taskman Agent <daniel+taskman@…>`) so authorship is never ambiguous in `git log`. Conventional-commit style encouraged via the prompt template, not enforced.

### 7.3 What crosses the boundary

Only `git_push` and `pr_open`. Credentials (a fine-grained PAT or deploy key per client org) live in the daemon's config, never in the sandbox, and never in a tool's arguments or results. Git host is confirmed as GitHub — every client repo checked has an `origin` of `git@github.com:Solvti/<repo>.git`, so PRs are opened via the GitHub API/`gh` against the `Solvti` org.

### 7.4 Protecting the read-only checkouts

Three layers, in order of reliability:

1. `git_pull_upstream` refuses to run if `odoo/` or `enterprise/` are dirty — so drift is *detected* every deploy.
2. No tool exists that can push those repos. Even a modified checkout cannot escape the machine.
3. A `pre-commit` hook installed in both checkouts that rejects all commits, as a tripwire.

Mount-level `ro` is the fourth and strongest option and is worth adopting if step 1 ever fires in practice.

---

## 8. Task Lifecycle

### 8.1 States

```
                    ┌──────────────────────────────────────┐
                    ▼                                      │
  (none) ──► refining ──► refined ──► awaiting_approval ───┤
                              │              │             │
                              │              ▼             │
                              │        implementing ──► implemented
                              │              │               │
                              │              ▼               ▼
                              └────────► (stopped) ───► completing ──► done
```

| State | Meaning | Buttons offered |
|---|---|---|
| *none* | Never seen by Taskman | **Refine Ticket** |
| `refining` | Agent analysing | **Monitor**, **Stop** |
| `refined` | Report received, awaiting human | **Monitor**, **Request Refinement**, **Request Approval**, **Implement** |
| `awaiting_approval` | Draft sent to PM; advisory only | **Implement**, **Monitor** |
| `implementing` | Agent coding | **Monitor**, **Stop** |
| `implemented` | Report received, branch pushed, draft PR open | **Monitor**, **Complete**, **Implement** *(re-run)* |
| `completing` | Capturing evidence, drafting summary | **Monitor** |
| `done` | Summary drafted into chatter | **Monitor** (read-only) |
| `interrupted` | Daemon restarted mid-phase | **Monitor**, **Resume**, **Stop** |

`awaiting_approval` does **not** gate `implementing` (I1: the gate is Daniel's judgement, not the software's). It exists so the button set and the Monitor header reflect reality, and so the audit log records that approval was requested.

### 8.2 Phase semantics

| Phase | Prompt seeded with | Ends when | Side effects on completion |
|---|---|---|---|
| **Refine** | ticket title + description (scraped), repo knowledge, refinement prompt template | `task_report(kind="refinement")` | `description_append` staged for auto-apply (§11.2); questions + criteria stored |
| **Implement** | refinement report, acceptance criteria, ticket text | `task_report(kind="implementation")` | branch pushed, draft PR opened |
| **Complete** | implementation report, PR url | `task_report(kind="completion")` | evidence captured; summary + screenshots staged as a chatter draft |

### 8.3 Stop and re-steer

`Stop` sends `SIGINT` to the harness process (which most CLI harnesses treat as "interrupt current turn cleanly, preserve session"), escalating to `SIGTERM` after 10s and container kill after 20s. The phase moves to `paused`, retaining `session_ref`.

Sending a message from Monitor while paused re-invokes the harness with its native resume mechanism plus the new message. Turn-based, per the agreed model — the daemon never writes to a live stdin.

---

## 9. Evidence Capture Skill

Lives at `.context/skills/odoo-evidence/`, symlinked into both harnesses' skill directories. Contents: a `SKILL.md` describing when and how to use it, and `capture.py` (Playwright).

Invocation is via the `evidence_capture` tool, which takes a step list rather than free-form code — because deterministic, replayable capture beats an agent improvising Playwright:

```json
{"name": "invoice-rounding-fixed",
 "steps": [
   {"goto": "/odoo/action-account.action_move_out_invoice_type"},
   {"login": "admin"},
   {"click_text": "INV/2026/0042"},
   {"wait_for_text": "Total 1,234.56"},
   {"screenshot": "invoice-total-correct", "caption": "Rounded total now matches the expected 1,234.56"}
 ]}
```

The runner handles login against the local test instance, viewport sizing (1440×900, deterministic for diffable screenshots), and writes PNGs to `.context/tasks/<n>/evidence/`, registering each with the daemon.

Screenshots are always of the **local test instance**, never the ticketing Odoo. Captions are agent-authored and are shown to Daniel for editing before anything is attached.

---

## 10. Browser Extension

Manifest V3. Deliberately the dumbest component in the system.

### 10.1 Architecture

- **Content script** on the ticketing Odoo origin: scrapes, injects the button bar, writes drafts into the composer. Holds no secrets, makes no network calls.
- **Service worker**: the only thing that talks to the daemon. Holds the bearer token in `chrome.storage.local`, keeps the WebSocket for Monitor, and relays via `chrome.runtime` messaging. Keeping the token out of the content script means a compromised or hostile page on that origin cannot read it.
- **Options page**: project→repo mapping and daemon token.
- **Monitor panel**: an injected side panel (or `chrome.sidePanel`), rendering the transcript stream, `task_note` breadcrumbs, deploy results, and the message box.

### 10.2 Scraping

Support both Odoo routing schemes, since instance versions vary:

- Odoo 17+ path routing: `/odoo/project/<project_id>/tasks/<task_id>` (and `/odoo/action-…/<id>`)
- Legacy hash routing: `/web#id=<id>&model=project.task&view_type=form`

Extract `{project_name, task_number, title, description_html}` from the DOM. Selectors are configuration, not code — a small selector map per Odoo version in the options page, so a UI change or a custom theme is a settings fix rather than a rebuild. If scraping fails, the button bar renders in a degraded state with a clear "couldn't read this page" message rather than guessing.

### 10.3 Project mapping

`chrome.storage.local`:

```json
{"Client A Support": {"repo": "client-a", "version": "17.0"},
 "Client B — Retainer": {"repo": "client-b", "version": "16.0"}}
```

Options page provides an editor; the daemon exposes `GET /repos` so the dropdown is populated from what actually exists on disk rather than free-typed.

### 10.4 Daemon HTTP API (extension-facing)

```
GET  /repos                              → discovered repos + versions
GET  /task/<id>                          → state, buttons, latest report
POST /task/<id>/refine                   → start refine phase
POST /task/<id>/implement
POST /task/<id>/complete
POST /task/<id>/stop
POST /task/<id>/message   {text}         → re-steer
POST /task/<id>/report/<rid>/applied     → mark a report as accepted into Odoo
GET  /task/<id>/evidence/<eid>           → PNG bytes
WS   /task/<id>/stream                   → transcript + state changes
```

---

## 11. Write-back Mechanics

The extension writes to Odoo purely through the DOM, as agreed. Two distinct mechanisms:

### 11.1 Chatter drafts (Request Refinement / Request Approval / Complete)

1. Click "Send message" to open the composer.
2. Set the composer's content and dispatch an `input` event so Odoo's reactive framework registers the change (setting `.value` alone will not).
3. For attachments, build a `DataTransfer` containing the evidence PNGs (fetched from the daemon as blobs) and assign it to the composer's hidden `input[type=file]`, then dispatch `change`.
4. **Stop.** Never click Send. The draft sits there for Daniel to read, edit, and send.

Message bodies come from templates in `~/.taskman/templates/`, overridable per repo at `.context/prompts/`. Default for Request Refinement, matching Daniel's phrasing:

> The description has been updated with a refined specification and acceptance criteria. A few questions came up during analysis:
> {questions}
> Please review the updated description, answer where you can, and confirm so we can proceed.

### 11.2 Description append (automatic, on view)

Applied lazily by the content script on page load when a `refined` report exists that has not been applied.

- **Append-only.** The original reporter's text is never modified. The refinement is added below a horizontal rule.
- **Idempotent.** The appended block is wrapped in `<div data-taskman-block="refinement-<report_id>">…</div>`; if that attribute is already present, the script does nothing. This survives page reloads, description edits by others, and re-running refinement (which produces a new report id and appends a second, clearly-delimited block).
- Applied by writing to the description field's editor and triggering Odoo's save, then `POST /report/<rid>/applied`.

The block carries a visible, human-readable header — `Refined specification — generated by Taskman, {date}` — because a client or PM reading the ticket deserves to know which prose came from a machine. This is a small thing that matters for trust.

---

## 12. Harness Adapter

A narrow interface, two implementations. Everything harness-specific lives behind it.

```python
class Harness(Protocol):
    name: str
    def start(self, *, cwd: Path, prompt: str, mcp_config: dict,
              env: dict) -> Session: ...
    def resume(self, session: Session, message: str) -> Session: ...
    def stop(self, session: Session) -> None: ...
    def transcript_events(self, session: Session) -> Iterator[Event]: ...
```

`Event` is normalised to `{type: text|tool_call|tool_result|error|done, ...}` so Monitor renders identically regardless of harness. Each adapter is responsible for translating its CLI's native streaming output into that shape.

Harness choice per phase, defaulting from `~/.taskman/config.yaml`, overridable per repo and per invocation — which makes it easy to try "refine with one, implement with the other" without any plumbing changes.

**Validate the asymmetry early.** The two tools' resume semantics and streaming formats are the least-known part of this design; §16 puts adapter validation at step 3 specifically so the interface is shaped by two real implementations rather than one plus an assumption.

---

## 13. Failure Modes

| Failure | Behaviour |
|---|---|
| Daemon down | Extension renders buttons disabled with "Taskman daemon not reachable"; no silent no-ops |
| Daemon restart mid-phase | Task → `interrupted`; container reconciled; Resume offered |
| Deploy fails to load modules | `DeployResult.ok = false` with parsed errors; agent iterates; Monitor shows the error prominently |
| Odoo container unhealthy | `odoo_status` reports it; `odoo_restart` available; three consecutive failures → surface to human, stop retrying |
| Agent exits unexpectedly | Phase → `failed`, last 50 lines in Monitor |
| Repo locked by another task | Button click returns which task holds it |
| Scrape fails | Degraded bar + explicit message; no guessing at project/task identity |
| Upstream checkout dirty | Deploy aborts loudly — never auto-discards changes in `odoo/` or `enterprise/` |
| Agent loops / burns tokens | **Human supervision only in v1.** Monitor shows elapsed time and turn count; Stop is always one click away |

On that last row: no timeout and no token cap, per the agreed philosophy. The mitigation is that Monitor makes a stuck run *visible* — elapsed time and turn count in the panel header are cheap to implement and are what actually make supervision work in practice.

---

## 14. Open Items

Carried from the plan, plus what surfaced while specifying:

1. **Does the local test env ever hold real or anonymized client data?** `odoo_shell` is unrestricted ORM access; the justification for that is entirely "the data is disposable." If real data can land there, `odoo_shell` needs at minimum prominent audit treatment, and possibly a read-only default with an explicit write flag.
2. **Git host and PR mechanics** — provider confirmed as GitHub, org `Solvti` (every client repo's `origin` is `git@github.com:Solvti/<repo>.git`), and draft PRs are supported there. Still open: token scoping model (one fine-grained PAT per client repo vs. one broad org token).
3. **opencode's instruction-file and skill-directory conventions** — verify against the installed version before committing to the `AGENTS.md` symlink; if it differs, the symlink target changes but nothing else does.
4. **Ticketing Odoo version and theme** — determines the selector map and routing pattern. Needs one real ticket page inspected.
5. **Whether `.context` is welcome in client repos** — if a client would find it odd in a PR diff, use the sibling-directory fallback (§3.2) for those repos.
6. **Odoo container base image** — confirmed: `ghcr.io/solvti/odoo-env:<version>`, the same custom image the existing `odoo-env-<version>` dev environments already build from and run Odoo from mounted source with (not official `odoo:17`/`odoo:18`). This is already the faithful option; no decision left here, just wiring the compose template to it.

---

## 15. Explicit Non-goals for v1

Stated so they don't creep in: autonomous/event-driven triggering; enforced approval gates; multi-user or team access; remote/multi-machine operation; cross-client isolation; token or cost budgets; production Odoo access of any kind; more than one task per repo concurrently; a third harness.

---

## 16. Build Sequence

| # | Milestone | Done when |
|---|---|---|
| 1 | Daemon skeleton + Docker verbs | `odoo_deploy` completes a real `-u` cycle on one repo from a CLI test client, with parsed errors |
| 2 | MCP server + per-task tokens | Claude Code, running in the sandbox, deploys and reads logs entirely through tools |
| 3 | Second harness adapter | opencode does the same; adapter interface survives contact with both |
| 4 | Git/PR tools + audit log | Draft PR opened end-to-end with no credential in the sandbox |
| 5 | State machine + `task_report` | Full refine→implement lifecycle drivable from `curl`, no UI |
| 6 | Extension v0 | Scrapes a real ticket, renders correct state-conditional buttons |
| 7 | Monitor panel | Live transcript, breadcrumbs, stop, re-steer |
| 8 | Write-back | Description append (idempotent) + chatter drafts with attachments |
| 9 | Evidence skill | Complete produces real screenshots into a chatter draft |
| 10 | Rollout | Remaining clients mapped; two concurrent tasks on different repos verified clean |

Steps 1–5 are a usable system without any UI, and that ordering is deliberate: the daemon and tool surface are where the risk lives, and they should be proven before a single line of extension code is written.

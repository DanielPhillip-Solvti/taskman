# Taskman — Revised Plan (v2, code-driven)

Supersedes the earlier Python-daemon/MCP-sandbox design (kept for reference on
branch `legacy-python-daemon`). That design over-engineered a privilege
boundary (untrusted sandbox, MCP tool surface, per-task tokens) for a
single-operator tool where none of that is actually needed today: the CLI
agent already runs with Daniel's own credentials inside his own dev
container. This version optimizes for "simplest thing that works."

## Core principle

**Code orchestrates. The agent is a tool call, not the orchestrator.**

Fetching repos, deciding what branch to check out, and wiring the harness
invocation together are all plain Go code with no LLM in the loop. The only
place an LLM runs is *inside* a single `docker exec ... claude -p "<prompt>"`
call that the Go code issues and waits on (or streams from) — the daemon
never asks an agent to plan its own orchestration.

## Components

1. **`taskmand`** — one Go binary, one process, no external DB (state is a
   JSON file + per-task log files under `~/.taskman/`). Exposes a small HTTP
   API on `127.0.0.1:8765` for the browser extension.
2. **Chrome extension (MV3)** — scrapes the ticketing Odoo task page (real
   DOM confirmed against a live instance, see `extension/README.md`),
   injects a button bar, calls the daemon over `localhost` HTTP.
3. **Existing `odoo-env-<version>` containers** — untouched. Daniel's
   `odoo-env-19-odoo-1` / `odoo-env-18-odoo-1` etc. already have Claude Code
   installed and a repo checkout bind-mounted at `/code` inside the
   container (host `odoo-env-<version>/` ↔ container `/code`). Taskman
   drives the agent by `docker exec`-ing into *that same, already-running*
   container — it does not create or manage any container of its own.

```
[Ticketing Odoo, remote]
  content script: scrape + inject buttons
        |
[Chrome extension service worker] --HTTP localhost:8765--> [taskmand]
                                                                |
                                          git clone/pull (FetchRepo) --> odoo-env-<v>/repos/<name>/
                                          docker exec (QueueTask*)   --> odoo-env-<v>-odoo-1: claude -p "<prompt>"
                                                                          cwd /code/repos/<name>
```

## Go API surface (matches the sketch given)

```go
// Config
GetHarnessList() []string
SetHarness(harness string) error
GetModelList() []string
SetModel(model string) error

// Work
FetchRepo(url, odooVersion string) (Repo, error)
QueueTaskRefinement(number int, repoName, description string) (TaskID, error)
QueueTask(number int, repoName, description string) (TaskID, error)
GetTaskOutput(number int) (TaskOutput, error)
InterruptTask(number int) error
```

- `FetchRepo` clones (first time) or fast-forward pulls (subsequent calls)
  the repo into `odoo-env-<major>/repos/<name>/` on the host, and records
  `{name, git_url, odoo_version}` in `~/.taskman/repos.json`. Purely git +
  filesystem, no agent involved.
- `QueueTaskRefinement` / `QueueTask` both resolve the repo's container
  (`odoo-env-<major>-odoo-1`) and version root, build a prompt from a fixed
  template (refinement vs. implementation), and run
  `docker exec -w /code/repos/<name> <container> claude -p "<prompt>" --output-format stream-json`
  in a goroutine, one task at a time per repo (a mutex per repo name — a
  second task on a busy repo is rejected with the holder's task number).
  Combined stdout/stderr is appended live to
  `~/.taskman/tasks/<number>.log`.
- `GetTaskOutput` returns `{status: queued|running|done|failed|interrupted,
  log: string}` — currently the whole log; fine at this log size, revisit if
  it gets unwieldy.
- `InterruptTask` sends `SIGTERM` to the tracked process, escalating to
  `SIGKILL` after 10s, matching the earlier design's stop semantics.

## What got dropped from the old design, deliberately

- No sandbox container, no MCP server, no per-task tokens, no audit log as a
  separate privileged subsystem — the agent already has Daniel's Docker
  access and git credentials by virtue of running inside his own dev
  container, so mediating that would be security theater here.
- No SQLite — a handful of repos and a handful of concurrent tasks fits in a
  JSON file without ceremony.
- No "sandbox has no git creds" boundary — not applicable, same reasoning.
- Real GitHub PR creation / git push are **not** built yet in this pass; the
  agent commits locally (it has real git access in-container) and Daniel
  pushes/PRs himself for now. Can be added as a thin `git push` code-path
  later without changing the shape above.

## Browser extension

See `extension/README.md` for the confirmed selector map, taken from a real
task page (`Solvti Sp. z o. o.` project ticketing instance, Odoo 19-ish path
routing `/odoo/action-<id>/<project_id>` and `/odoo/<model>/<id>`).

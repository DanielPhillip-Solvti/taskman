# taskman

Chrome extension + local daemon that drives a CLI coding agent (Claude Code)
against your Odoo tickets. See [docs/PLAN.md](docs/PLAN.md) for the
architecture and [docs/data-flow.md](docs/data-flow.md) for a sequence
diagram of a request end to end.

## Philosophy & isolation

**Code orchestrates, the agent doesn't.** Fetching repos, picking branches,
and building the prompt are plain Go — the LLM only ever runs inside one
`docker exec … claude -p "<prompt>"` call the daemon issues and waits on.
It's never handed the keys to plan its own next steps.

**No sandbox, by design.** The agent runs with your own credentials inside
your own dev container — the same git and Docker access you already have —
rather than behind a separate privilege boundary. That's a deliberate
trade-off for a single-operator tool, not an oversight: there's no
meaningfully different trust level between you and the agent to isolate.
Ticket content (title, description, chatter) is still treated as untrusted
input and explicitly framed as data, not instructions, when it's handed to
the agent — the isolation that matters here is prompt-injection resistance,
not filesystem/network sandboxing.

## Install

Two pieces, install in either order — each one detects if the other is
missing and tells you what to do.

### 1. The daemon (`taskmand`)

```bash
curl -fsSL https://raw.githubusercontent.com/DanielPhillip-Solvti/taskman/main/scripts/install.sh | sh
```

Downloads the latest release binary for your OS/arch, installs it to
`~/.local/bin`, and registers it to start on login (launchd on macOS,
systemd --user on Linux). Re-run it any time to update.

Prebuilt binaries come from [Releases](https://github.com/DanielPhillip-Solvti/taskman/releases),
built by [`.github/workflows/release.yml`](.github/workflows/release.yml) on
every tag push.

Once installed, control the service with `taskmanctl` (also installed to
`~/.local/bin`), which wraps launchd/systemd so you don't need to know which
one you're on:

```bash
taskmanctl start|stop|restart|status|logs
```

### 2. The extension

1. Download the [latest extension zip](https://github.com/DanielPhillip-Solvti/taskman/releases/latest) (or `cd extension && zip -r ../taskman-extension.zip .` from source).
2. Unzip it somewhere permanent.
3. In Chrome, go to `chrome://extensions`, enable **Developer mode** (top right), click **Load unpacked**, and select the unzipped folder.
4. Click the taskman icon in the toolbar — it checks for `taskmand` and walks you through installing it if it's not running yet.
5. Open an Odoo ticket. If its project isn't mapped to a repo yet, the panel prompts for that via **Configure…**.

Whichever half you install first, the other one's UI tells you what's
missing — there's no fixed order.

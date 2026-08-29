# taskman

Chrome extension + local daemon that drives a CLI coding agent (Claude Code)
against your Odoo tickets. See [docs/PLAN.md](docs/PLAN.md) for the
architecture and [docs/data-flow.md](docs/data-flow.md) for a sequence
diagram of a request end to end.

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

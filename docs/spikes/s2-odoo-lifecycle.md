# S2 — Odoo process lifecycle inside a warm container

## What was available to probe

No `ghcr.io/solvti/odoo-env:<version>` pull succeeds in this sandbox (see docs/DECISIONS.md —
no registry credentials/route). `/code/odoo-env-19` and `/code/odoo-env-18` are real, running
checkouts but are Daniel's live dev environment and are explicitly off-limits to touch, exec
into, or restart (task guardrails). So this spike is **static inspection of the real
`odoo-env-19` scripts and config, not a dynamic warm-cycle measurement against a real Odoo
process.** That measurement is deferred to Milestone 1, gated on either real ghcr credentials
or building a full Odoo image from source inside the fallback base (both out of scope for a
timeboxed spike).

## What was confirmed statically

From `/code/odoo-env-19/start`, `/code/odoo-env-19/shell`, `/code/odoo-env-19/ln` (read-only,
verbatim):

```sh
# start
odoo -c odoo.conf $@
# shell
odoo shell -c odoo.conf $@
# ln
rm odoo.conf; ln -s $1/odoo.conf /code/odoo.conf
```

This confirms the spec §6.1 model exactly: the *container* just stays up (compose's `command:
bash`, `tty: true` in `docker-compose.yml` — there is no long-running `odoo-bin` in the
container's own entrypoint), and `odoo-bin` (via the `odoo` wrapper on PATH) is launched and
killed as a foreground process inside it, per-invocation, driven by `./start` / `./bash` +
manual commands. Which client is active is controlled by re-pointing the `odoo.conf` symlink
(`./ln repos/<repo>`), not by container config — confirming spec §6.1's departure note that
Taskman's one-container-per-repo model is new infrastructure, not a description of today's
setup.

`docker-compose.yml`'s odoo service: base image `odoo-env:19.0` (built `FROM
ghcr.io/solvti/odoo-env:19.0`), Postgres via `PGHOST`/`PGPASSWORD` env vars, ports 8069→8169 and
8072→8172. Taskman's generated per-repo compose template (§6.1) should mirror this shape
(env-based PG connection, not a conf-embedded password) but bind to container-internal 8069
only, reached over `taskman-net` by container name rather than a published host port, since
concurrent per-repo containers can't all claim the same host port.

## What remains unverified (carried to Milestone 1)

- Exact warm-cycle duration for `pkill -TERM -f odoo-bin` + relaunch with `-u <module>` on a
  real Odoo 19 boot.
- Exact readiness log line (spec assumes something like `Odoo version 19.0` / HTTP service
  listening; must capture verbatim once a real image is buildable).
- Exact failure log line shapes for `ParseError`, `ImportError`, manifest dependency errors,
  `psycopg2` constraint violations — needed for `fixtures/odoo-logs/` (brief §4.3) and the
  golden-file parser tests. These **cannot** be fabricated per the brief's own reasoning ("a
  real ParseError from a real Odoo boot is worth more than a mocked one") — they must come from
  a real boot once the image question is resolved.
- Confirmation that `pkill -TERM -f odoo-bin` leaves Postgres and the container healthy across
  ~20 cycles.

## Recommendation

Milestone 1's gate must include a real container build (fallback base image per
docs/DECISIONS.md, or the real ghcr image if credentials arrive first) and cannot be
satisfied by static inspection alone. This spike's job — confirming the *mechanism* (sleeping
container + repeatedly-restarted foreground process, conf-symlink-style version/repo
selection) — is done; the *numbers* (cycle time, exact log lines) are Milestone 1 work.

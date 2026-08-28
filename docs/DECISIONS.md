# Decisions Log

Every ambiguity resolved during the build, in chronological order.

## 2026-08-28 — client repo module directory is `addons/`, not `custom-addons/`

`technical-spec.md` (§3.1, §3.3, §6.1, §6.2) assumed a client repo's own modules live under
`custom-addons/`. Verified against the real checkouts:

```
$ ls /code/odoo-env-19/repos/activeconnect/
LICENSE  README.md  addons  example.odoo.conf  external-addons  requirements.txt
```

Every repo under `/code/odoo-env-19/repos/*` and `/code/odoo-env-18/repos/*` uses `addons/` +
`external-addons/` (+ optionally `oca-addons/`). There is no `custom-addons/` anywhere.

**Fix applied in this commit:** `docs/technical-spec.md` updated in place, s/custom-addons/addons/
everywhere it appeared (repo layout diagram, `taskman.yaml` comment, addons_path assembly
prose, and the module-detection glob). No other spec content changed.

## 2026-08-28 — sandbox/Odoo base image substituted

Spec §2.1/§6.1 call for `ghcr.io/solvti/odoo-env:<version>` as the base image. This sandbox has
no `ghcr.io` credentials configured; `docker pull ghcr.io/solvti/odoo-env:19.0` does not
complete (times out — no auth, and possibly no route to ghcr.io at all). Docker Hub is
reachable (`docker pull python:3.12-slim` succeeds).

**Fallback for this build:** Milestone 1+ Odoo container images build from `python:3.12-slim`
plus whatever is observably needed (matching `/code/odoo-env-19/Dockerfile`'s apt layer:
locales, the `odoo` user/group dance is irrelevant off that base and is dropped). The Dockerfile
takes the base image as a single `ARG BASE_IMAGE` at the top so swapping back to the real
`ghcr.io/solvti/odoo-env:<version>` once credentials exist is a one-line change (rebuild with
`--build-arg BASE_IMAGE=ghcr.io/solvti/odoo-env:19.0`). Documented here per the guardrail in
implementation-brief.md §7.

## 2026-08-28 — no GitHub credentials available

Spec §7.3 / brief §4.3 `pr_open` needs a real forge. No PAT is available in this sandbox.
Per implementation-brief.md §7 guardrail ("Tests that need a forge use a local scratch remote"),
`git_push`/`pr_open` are implemented against a local bare git repo created per-test under a temp
dir, with a minimal local "forge" stub that records PR-like metadata (title/body/branch) to a
JSON file the gate can assert against. Real GitHub PR creation is unverified pending a real
token — noted again in BLOCKERS.md when Milestone 4 is reached.

## 2026-08-28 — no ticketing Odoo instance for S3

Spike S3 (browser injection) has no real client-facing ticketing Odoo instance to probe against.
Findings in `docs/spikes/s3-ui-injection.md` are derived from spec §10.2's assumptions plus a
locally-served fixture HTML page built to mimic Odoo 17+ DOM shapes, and are explicitly marked
unvalidated against a real instance.

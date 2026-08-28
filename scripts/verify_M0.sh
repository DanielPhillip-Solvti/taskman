#!/usr/bin/env bash
# Milestone 0 gate (implementation-brief.md §6):
#   - Daemon starts, /health responds
#   - fixture repos exist
#   - all three spike docs present
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FAIL=0
fail() { echo "[FAIL] $1"; FAIL=1; }
pass() { echo "[ OK ] $1"; }

PY="$ROOT/.venv/bin/python"
if [ ! -x "$PY" ]; then
  echo "[FAIL] $PY not found — run: uv venv .venv && uv pip install -e '.[dev]' --python .venv/bin/python"
  exit 1
fi

# --- unit tests must pass first (fast, no docker) -------------------------
if "$PY" -m pytest tests/unit -q; then
  pass "unit tests"
else
  fail "unit tests"
fi

# --- daemon starts, /health responds, from a clean temp home --------------
TASKMAN_HOME="$(mktemp -d)"
PORT=18765
export TASKMAN_HOME
"$PY" -c "
import uvicorn
from daemon.app import create_app
from daemon.config import TaskmanConfig
cfg = TaskmanConfig()
app = create_app(cfg)
uvicorn.run(app, host='127.0.0.1', port=$PORT, log_level='warning')
" &
DAEMON_PID=$!
trap 'kill "$DAEMON_PID" 2>/dev/null; wait "$DAEMON_PID" 2>/dev/null; rm -rf "$TASKMAN_HOME"' EXIT

READY=0
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$PORT/health" >/tmp/taskman_health.$$ 2>/dev/null; then
    READY=1
    break
  fi
  sleep 0.3
done

if [ "$READY" = "1" ]; then
  BODY="$(cat /tmp/taskman_health.$$)"
  rm -f /tmp/taskman_health.$$
  if echo "$BODY" | grep -q '"ok":true' && echo "$BODY" | grep -q '"db":true'; then
    pass "/health responds ok, db initialised ($BODY)"
  else
    fail "/health responded but not healthy: $BODY"
  fi
else
  fail "/health did not respond within timeout"
fi

if [ -f "$TASKMAN_HOME/state.db" ]; then
  pass "state.db created under TASKMAN_HOME"
else
  fail "state.db not created"
fi

# --- fixture repos exist ---------------------------------------------------
for p in \
  "fixtures/repos/17.0/odoo/odoo-bin" \
  "fixtures/repos/17.0/enterprise/account_accountant/__manifest__.py" \
  "fixtures/repos/17.0/demo_client/addons/taskman_demo/__manifest__.py"; do
  if [ -e "$p" ]; then
    pass "fixture present: $p"
  else
    fail "fixture missing: $p (run scripts/bootstrap_fixtures.sh)"
  fi
done

if git -C fixtures/repos/17.0/demo_client rev-parse --verify broken >/dev/null 2>&1; then
  pass "demo_client has a broken branch"
else
  fail "demo_client missing the deliberately-broken branch"
fi

# --- spike docs present -----------------------------------------------------
for p in \
  "docs/spikes/s1-harness-sessions.md" \
  "docs/spikes/s2-odoo-lifecycle.md" \
  "docs/spikes/s3-ui-injection.md"; do
  if [ -s "$p" ]; then
    pass "spike doc present: $p"
  else
    fail "spike doc missing or empty: $p"
  fi
done

# --- repo layout / doc conventions from brief §3 ---------------------------
for p in "IMPLEMENTATION_BRIEF.md" "CLAUDE.md" "AGENTS.md" "docs/architecture-plan.md" "docs/technical-spec.md" "docs/DECISIONS.md"; do
  if [ -e "$p" ]; then
    pass "layout: $p exists"
  else
    fail "layout: $p missing"
  fi
done

if [ "$FAIL" -eq 0 ]; then
  echo "== Milestone 0: PASS =="
  exit 0
else
  echo "== Milestone 0: FAIL =="
  exit 1
fi

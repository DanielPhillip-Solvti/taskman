#!/usr/bin/env bash
# Executable gate for the taskmand daemon. Sets up its own scratch state
# (fresh TASKMAN_HOME, a scratch bare git remote, a throwaway Docker
# container standing in for a "odoo-env-<v>-odoo-1" dev container with a
# stub `claude` binary), exercises the real HTTP API end to end, tears
# down, and exits 0/1 with a readable reason. Never touches a real
# odoo-env-* checkout or a real odoo-env-*-odoo-1 container.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ -d "$ROOT/extension/node_modules" ]; then
  echo "[verify] running extension scrape test"
  (cd "$ROOT/extension" && npm test) || { echo "[verify] FAIL: extension scrape test"; exit 1; }
else
  echo "[verify] skipping extension scrape test (run 'npm install' in extension/ first)"
fi

WORK="$(mktemp -d /tmp/taskman-verify.XXXXXX)"
FAKE_VERSION="989.0"
FAKE_ENV_ROOT="/code/odoo-env-989"
FAKE_CONTAINER="odoo-env-989-odoo-1"
ADDR="127.0.0.1:8799"
PASS=1

log()  { echo "[verify] $*"; }
fail() { echo "[verify] FAIL: $*"; PASS=0; }

cleanup() {
  [ -n "${DAEMON_PID:-}" ] && kill "$DAEMON_PID" >/dev/null 2>&1
  docker rm -f "$FAKE_CONTAINER" >/dev/null 2>&1
  rm -rf "$FAKE_ENV_ROOT" "$WORK"
}
trap cleanup EXIT

log "building taskmand"
(cd "$ROOT" && go build -o "$WORK/taskmand" ./cmd/taskmand) || { fail "build failed"; exit 1; }

log "setting up scratch git remote"
git init -q --bare "$WORK/scratch.git"
git clone -q "$WORK/scratch.git" "$WORK/seed"
(cd "$WORK/seed" && git -c user.email=v@v.v -c user.name=Verify commit -q --allow-empty -m seed && git push -q origin master) >/dev/null 2>&1

log "setting up fake odoo-env root + stub dev container ($FAKE_CONTAINER)"
mkdir -p "$FAKE_ENV_ROOT/repos"
docker run -d --rm --name "$FAKE_CONTAINER" -v "$FAKE_ENV_ROOT:/code" -w / alpine:latest sleep 300 >/dev/null

# Stub `claude` binary: a real script (not agent-authored) that plays the
# part of the harness CLI, distinguishing the "write a PR summary" prompt
# from any other prompt so the implement flow's summary step is verifiable.
cat > "$WORK/claude-stub.sh" <<'STUB'
#!/bin/sh
shift
prompt="$*"
case "$prompt" in
  *"pull-request summary"*) echo "STUB_SUMMARY: did the thing" ;;
  *) echo "STUB_AGENT: prompt length ${#prompt}" ;;
esac
STUB
chmod +x "$WORK/claude-stub.sh"
docker cp "$WORK/claude-stub.sh" "$FAKE_CONTAINER:/usr/local/bin/claude"

log "starting daemon (home=$WORK/home)"
TASKMAN_HOME="$WORK/home" TASKMAN_ADDR="$ADDR" "$WORK/taskmand" > "$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 20); do
  curl -fs "http://$ADDR/health" >/dev/null 2>&1 && break
  sleep 0.25
done

curl -fs "http://$ADDR/health" | grep -q '"ok":true' \
  && log "health OK" || fail "daemon did not become healthy"

log "config: default settings"
curl -fs "http://$ADDR/config" | grep -q '"harness":"claude"' \
  && log "default harness is claude" || fail "unexpected default config"

log "config: switch harness/model"
curl -fs -X POST "http://$ADDR/config/harness" -d '{"harness":"opencode"}' | grep -q '"harness":"opencode"' \
  && log "harness switch OK" || fail "harness switch failed"
curl -fs -X POST "http://$ADDR/config/harness" -d '{"harness":"claude"}' >/dev/null

log "config: reject unknown harness"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/config/harness" -d '{"harness":"nope"}')
[ "$CODE" = "400" ] && log "unknown harness rejected (400)" || fail "expected 400 for unknown harness, got $CODE"

log "repos: fetch (clone) then fetch again (pull)"
FETCH1=$(curl -fs -X POST "http://$ADDR/repos/fetch" -d "{\"url\":\"$WORK/scratch.git\",\"odoo_version\":\"$FAKE_VERSION\"}")
echo "$FETCH1" | grep -q '"cloned":true' && log "clone OK" || fail "expected cloned:true, got: $FETCH1"
FETCH2=$(curl -fs -X POST "http://$ADDR/repos/fetch" -d "{\"url\":\"$WORK/scratch.git\",\"odoo_version\":\"$FAKE_VERSION\"}")
echo "$FETCH2" | grep -q '"pulled":true' && log "pull OK" || fail "expected pulled:true, got: $FETCH2"
[ -d "$FAKE_ENV_ROOT/repos/scratch" ] && log "repo present on disk" || fail "repo not cloned to expected path"

log "repos: reject fetch into a non-existent odoo-env root"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/repos/fetch" -d "{\"url\":\"$WORK/scratch.git\",\"odoo_version\":\"777.0\"}")
[ "$CODE" = "500" ] && log "missing env root rejected" || fail "expected 500 for missing env root, got $CODE"

log "tasks: unknown repo is rejected"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/tasks/1/refine" -d '{"repo_name":"nope","description":"x"}')
[ "$CODE" = "400" ] && log "unknown repo rejected (400)" || fail "expected 400 for unknown repo, got $CODE"

log "tasks: queue refinement against stub container, poll to completion"
curl -fs -X POST "http://$ADDR/tasks/42/refine" -d '{"repo_name":"scratch","description":"the button is the wrong color"}' | grep -q '"status":"queued"' \
  && log "task queued" || fail "queue did not return queued status"

DONE=0
for _ in $(seq 1 40); do
  OUT=$(curl -fs "http://$ADDR/tasks/42/output")
  case "$OUT" in
    *'"status":"done"'*) DONE=1; break ;;
    *'"status":"failed"'*) fail "task 42 failed: $OUT"; DONE=1; break ;;
  esac
  sleep 0.25
done
[ "$DONE" = "1" ] || fail "task 42 never reached a terminal state"
echo "$OUT" | grep -q 'STUB_AGENT' && log "stub harness output captured in log" || fail "expected stub output in task log, got: $OUT"

log "tasks: second task on a busy repo is refused with the holder's id"
curl -fs -X POST "http://$ADDR/tasks/43/refine" -d '{"repo_name":"scratch","description":"slow one"}' >/dev/null
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/tasks/44/refine" -d '{"repo_name":"scratch","description":"conflict"}')
[ "$CODE" = "409" ] && log "second task on busy repo rejected (409)" || fail "expected 409 for busy repo, got $CODE"
curl -fs -X POST "http://$ADDR/tasks/43/interrupt" >/dev/null

log "tasks: interrupt is reflected in output"
for _ in $(seq 1 20); do
  curl -fs "http://$ADDR/tasks/43/output" | grep -q '"status":"interrupted"' && break
  sleep 0.25
done
curl -fs "http://$ADDR/tasks/43/output" | grep -q '"status":"interrupted"' \
  && log "interrupt reflected" || fail "task 43 was not marked interrupted"

log "tasks: implement flow — pulls main, creates branch, delegates, summarizes, pushes, opens PR"
# Task 43's interrupt (above) marks it "interrupted" synchronously but the
# repo lock only releases once the SIGTERM'd process actually exits, which
# is asynchronous — so retry briefly instead of assuming it's released the
# instant the interrupt call returns.
IMPLEMENT_RES=""
for _ in $(seq 1 40); do
  IMPLEMENT_RES=$(curl -s -X POST "http://$ADDR/tasks/50/implement" -d '{"repo_name":"scratch","title":"Fix Widget Color","description":"the button is the wrong color"}')
  echo "$IMPLEMENT_RES" | grep -q '"status":"queued"' && break
  sleep 0.25
done
echo "$IMPLEMENT_RES" | grep -q '"status":"queued"' && log "implement queued" || fail "implement did not queue: $IMPLEMENT_RES"

DONE=0
for _ in $(seq 1 80); do
  OUT=$(curl -fs "http://$ADDR/tasks/50/output")
  case "$OUT" in
    *'"status":"done"'*) DONE=1; break ;;
    *'"status":"failed"'*) fail "task 50 failed: $OUT"; DONE=1; break ;;
  esac
  sleep 0.25
done
[ "$DONE" = "1" ] || fail "task 50 never reached a terminal state (last seen: $OUT)"

echo "$OUT" | grep -q '"branch":"task/50-fix-widget-color"' \
  && log "branch name derived from task number + title" || fail "unexpected/missing branch in output: $OUT"
echo "$OUT" | grep -q 'STUB_SUMMARY' \
  && log "agent-authored summary captured" || fail "expected summary text in output: $OUT"

(cd "$FAKE_ENV_ROOT/repos/scratch" && git rev-parse --verify task/50-fix-widget-color >/dev/null 2>&1) \
  && log "task branch exists locally" || fail "task branch was not created in the repo"
git --git-dir="$WORK/scratch.git" branch --list 'task/50-*' | grep -q 'task/50' \
  && log "task branch was pushed to the remote" || fail "task branch was not pushed to the scratch remote"

SUMMARY_FILE="$WORK/home/tasks/50.summary.md"
[ -s "$SUMMARY_FILE" ] && log "summary written to its own file (50.summary.md)" || fail "expected non-empty $SUMMARY_FILE"
grep -q 'STUB_SUMMARY' "$SUMMARY_FILE" && log "summary file content matches captured summary" || fail "summary file content unexpected"

# The scratch remote is a local bare repo, not GitHub, so `gh pr create`
# necessarily fails here — that's the expected shape: reported plainly,
# task still lands on Done because the implementation itself succeeded.
echo "$OUT" | grep -q 'PR creation failed' \
  && log "PR-creation failure surfaced (expected: scratch remote isn't a real GitHub repo)" || fail "expected PR-failure message in output: $OUT"

if [ "$PASS" = "1" ]; then
  echo "== taskman verify: PASS =="
  exit 0
else
  echo "== taskman verify: FAIL =="
  echo "--- daemon log ---"
  cat "$WORK/daemon.log"
  exit 1
fi

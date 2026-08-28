#!/usr/bin/env bash
# Milestone 1 Gate: Odoo env management + deploy pipeline + log parser
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 1. Cumulative check: verify M0 first
./scripts/verify_M0.sh

echo "== Verifying Milestone 1 =="

export TASKMAN_HOME="$(mktemp -d /tmp/taskman-test-m1-XXXXXX)"
trap 'rm -rf "$TASKMAN_HOME"' EXIT

# Run Python integration test against real DockerEngine & demo_client fixture
uv run python - << 'EOF'
import sys
import subprocess
from pathlib import Path
from daemon.docker.compose import RepoMounts
from daemon.docker.engine import RealDockerEngine
from daemon.odoo.deploy import deploy_modules

repo_dir = Path("fixtures/repos/17.0/demo_client").resolve()
odoo_dir = Path("fixtures/repos/17.0/odoo").resolve()
enterprise_dir = Path("fixtures/repos/17.0/enterprise").resolve()

mounts = RepoMounts(
    odoo_dir=odoo_dir,
    repo_dir=repo_dir,
    enterprise_dir=enterprise_dir,
)

engine = RealDockerEngine()
conf_dir = Path(f"{Path.home()}/.taskman/conf")

# Step 1: Deploy on clean branch ('main')
print("[ M1 ] Testing deploy on clean branch ('main')...")
subprocess.run(["git", "checkout", "-q", "main"], cwd=repo_dir, check=True)

res_clean = deploy_modules(
    engine,
    repo="test-demo-m1",
    mounts=mounts,
    conf_dir=conf_dir,
    modules=["taskman_demo"],
    timeout_s=120.0,
)

print(f"[ M1 ] Clean deploy result: ok={res_clean.ok}, modules={res_clean.modules_updated}")
if not res_clean.ok:
    print(f"[ FAIL ] Clean deploy failed with errors: {res_clean.errors}")
    print(f"Log tail:\n{res_clean.log_tail}")
    sys.exit(1)

assert res_clean.ok is True
assert "taskman_demo" in res_clean.modules_updated

# Step 2: Deploy on broken branch ('broken')
print("[ M1 ] Testing deploy on broken branch ('broken')...")
subprocess.run(["git", "checkout", "-q", "broken"], cwd=repo_dir, check=True)

try:
    res_broken = deploy_modules(
        engine,
        repo="test-demo-m1",
        mounts=mounts,
        conf_dir=conf_dir,
        modules=["taskman_demo"],
        timeout_s=120.0,
    )
    print(f"[ M1 ] Broken deploy result: ok={res_broken.ok}, errors={res_broken.errors}")
    if res_broken.ok:
        print("[ FAIL ] Expected broken deploy to return ok=False, but got ok=True")
        sys.exit(1)

    file_line_errs = [e for e in res_broken.errors if e.file or "ParseError" in (e.exc_type or "") or "Parse error" in e.message]
    if not file_line_errs:
        print(f"[ FAIL ] Expected parsed ParseError naming file/line, got: {res_broken.errors}")
        sys.exit(1)

    print(f"[ OK ] Parsed error correctly: {file_line_errs[0]}")
finally:
    subprocess.run(["git", "checkout", "-q", "main"], cwd=repo_dir, check=True)

print("== Milestone 1: PASS ==")
EOF

# Cleanup test container / stack
echo "[ M1 ] Cleaning up test container stack..."
docker compose -p taskman-test-demo-m1 down -v --remove-orphans >/dev/null 2>&1 || true

"""Odoo deployment pipeline (technical-spec.md §6.2).

Implements module change detection, docker-compose stack management, warm process restart,
and startup log polling with structured error parsing.
"""

from __future__ import annotations

import subprocess
import time
from pathlib import Path

from daemon.docker.compose import RepoMounts, render_compose, render_odoo_conf
from daemon.docker.engine import DockerEngine
from daemon.odoo.logparse import LogError, parse_log
from daemon.odoo.odoo_env import (
    create_database,
    database_exists,
    db_container_name,
    kill_odoo_process,
    odoo_container_name,
    wait_for_postgres,
)
from daemon.state.models import DeployResult

CONTAINER_CONF_PATH = "/etc/odoo/odoo.conf"
LOG_FILE_INSIDE_CONTAINER = "/tmp/odoo-taskman.log"


def determine_changed_modules(repo_dir: Path, base_branch: str = "master") -> list[str]:
    """Determines changed Odoo modules in repo_dir relative to base_branch and uncommitted files
    (technical-spec.md §6.2 step 4)."""
    changed_files: set[str] = set()

    # 1. git diff against base_branch
    try:
        res = subprocess.run(
            ["git", "diff", "--name-only", f"{base_branch}..HEAD"],
            cwd=repo_dir,
            capture_output=True,
            text=True,
            check=False,
        )
        if res.returncode == 0:
            changed_files.update(res.stdout.splitlines())
    except Exception:
        pass

    # 2. git status --porcelain
    try:
        res = subprocess.run(
            ["git", "status", "--porcelain", "--untracked-files=all"],
            cwd=repo_dir,
            capture_output=True,
            text=True,
            check=False,
        )
        if res.returncode == 0:
            for line in res.stdout.splitlines():
                if len(line) > 3:
                    file_path = line[3:].strip()
                    changed_files.add(file_path)
    except Exception:
        pass

    modules: set[str] = set()
    subdirs = ("addons", "external-addons", "oca-addons")

    for file_path_str in changed_files:
        parts = Path(file_path_str).parts
        if len(parts) >= 2 and parts[0] in subdirs:
            candidate_module = parts[1]
            manifest = repo_dir / parts[0] / candidate_module / "__manifest__.py"
            if manifest.is_file():
                modules.add(candidate_module)

    return sorted(modules)


def deploy_modules(
    engine: DockerEngine,
    *,
    repo: str,
    mounts: RepoMounts,
    conf_dir: Path,
    modules: list[str] | None = None,
    db_name: str | None = None,
    image: str = "ghcr.io/solvti/odoo-env:17.0",
    network: str = "taskman-test-net",
    timeout_s: float = 300.0,
    poll_interval_s: float = 0.5,
) -> DeployResult:
    """Executes an Odoo warm deploy cycle (technical-spec.md §6.2)."""
    start_time = time.monotonic()
    target_db = db_name or f"taskman_test_{repo}"

    # 1. Network setup
    net_res = engine.ensure_network(network)
    if not net_res.ok:
        return DeployResult(
            ok=False,
            duration_s=time.monotonic() - start_time,
            errors=[LogError(level="CRITICAL", message=f"Failed to create network {network}: {net_res.stderr}")],
        )

    # 2. Render configuration and compose definition
    conf_dir.mkdir(parents=True, exist_ok=True)
    conf_host_path = conf_dir / f"{repo}.conf"
    db_container = db_container_name(repo)
    odoo_container = odoo_container_name(repo)

    conf_content = render_odoo_conf(
        mounts,
        db_name=target_db,
        db_container=db_container,
    )
    conf_host_path.write_text(conf_content)

    compose_content = render_compose(
        repo=repo,
        image=image,
        network=network,
        mounts=mounts,
        conf_host_path=conf_host_path,
    )
    compose_host_path = conf_dir / f"{repo}-compose.yml"
    compose_host_path.write_text(compose_content)

    # 3. Bring compose stack up
    up_res = engine.run_compose(str(compose_host_path), "-p", f"taskman-{repo}", "up", "-d")
    if not up_res.ok:
        return DeployResult(
            ok=False,
            duration_s=time.monotonic() - start_time,
            errors=[LogError(level="CRITICAL", message=f"run_compose failed: {up_res.stderr}")],
        )

    # 4. Wait for Postgres readiness
    pg_wait = wait_for_postgres(
        engine,
        odoo_container=odoo_container,
        db_container=db_container,
        timeout_s=30.0,
    )
    if not pg_wait.ok:
        return DeployResult(
            ok=False,
            duration_s=time.monotonic() - start_time,
            errors=[LogError(level="CRITICAL", message=f"Postgres not ready: {pg_wait.detail}")],
        )

    # 5. Ensure database exists
    if not database_exists(engine, odoo_container=odoo_container, db_container=db_container, db_name=target_db):
        db_res = create_database(engine, odoo_container=odoo_container, db_container=db_container, db_name=target_db)
        if not db_res.ok:
            return DeployResult(
                ok=False,
                duration_s=time.monotonic() - start_time,
                errors=[LogError(level="CRITICAL", message=f"Failed to create DB {target_db}: {db_res.stderr}")],
            )

    # 6. Determine modules to update if not specified
    if modules is None:
        modules = determine_changed_modules(mounts.repo_dir)

    # 7. Kill running odoo-bin process
    kill_odoo_process(engine, odoo_container=odoo_container)

    # 8. Truncate log file inside container
    engine.exec_run(odoo_container, ["rm", "-f", LOG_FILE_INSIDE_CONTAINER])

    # 9. Start odoo-bin process detached
    cmd = [
        "odoo-bin",
        "-c", CONTAINER_CONF_PATH,
        "-d", target_db,
        f"--logfile={LOG_FILE_INSIDE_CONTAINER}",
    ]
    if modules:
        cmd.extend(["-u", ",".join(modules)])

    exec_start_res = engine.exec_detached(odoo_container, cmd)
    if not exec_start_res.ok:
        return DeployResult(
            ok=False,
            duration_s=time.monotonic() - start_time,
            modules_updated=modules,
            errors=[LogError(level="CRITICAL", message=f"Failed to exec odoo-bin: {exec_start_res.stderr}")],
        )

    # 10. Poll log file for readiness or error verdict
    poll_start = time.monotonic()
    last_parsed = None

    while time.monotonic() - poll_start < timeout_s:
        cat_res = engine.exec_run(odoo_container, ["cat", LOG_FILE_INSIDE_CONTAINER])
        if cat_res.ok and cat_res.stdout:
            last_parsed = parse_log(cat_res.stdout)
            if last_parsed.ok is True:
                return DeployResult(
                    ok=True,
                    duration_s=time.monotonic() - start_time,
                    modules_updated=modules,
                    errors=last_parsed.errors,
                    log_tail=last_parsed.log_tail,
                )
            if last_parsed.ok is False:
                return DeployResult(
                    ok=False,
                    duration_s=time.monotonic() - start_time,
                    modules_updated=modules,
                    errors=last_parsed.errors,
                    log_tail=last_parsed.log_tail,
                )
        time.sleep(poll_interval_s)

    # Timeout reached
    log_tail = last_parsed.log_tail if last_parsed else ""
    errors = last_parsed.errors if last_parsed else []
    if not errors:
        errors.append(LogError(level="CRITICAL", message="Deploy timed out waiting for Odoo startup signal"))

    return DeployResult(
        ok=False,
        duration_s=time.monotonic() - start_time,
        modules_updated=modules,
        errors=errors,
        log_tail=log_tail,
    )

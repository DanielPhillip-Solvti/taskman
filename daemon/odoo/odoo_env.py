"""Lifecycle helpers for the per-repo Odoo test environment (technical-spec.md §6.1).

Kept separate from deploy.py's orchestration logic so the "how do I talk to this
container" primitives (readiness waits, db existence checks, process kill) are
independently testable against the DockerEngine protocol/fake.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from daemon.docker.engine import DockerEngine, Result


def odoo_container_name(repo: str) -> str:
    return f"taskman-odoo-{repo}"


def db_container_name(repo: str) -> str:
    return f"taskman-db-{repo}"


@dataclass
class WaitResult:
    ok: bool
    waited_s: float
    detail: str = ""


def wait_for_postgres(engine: DockerEngine, *, odoo_container: str, db_container: str,
                       db_user: str = "odoo", db_password: str = "odoo",
                       timeout_s: float = 60.0, poll_s: float = 1.0) -> WaitResult:
    """Polls `pg_isready` from inside the odoo container (it has the postgres client
    tools baked in — see docker/odoo-test/Dockerfile) against the db container, since
    that's exactly the network path Odoo itself will use."""
    start = time.monotonic()
    last: Result = Result(ok=False, stderr="never attempted")
    while time.monotonic() - start < timeout_s:
        last = engine.exec_run(
            odoo_container,
            ["pg_isready", "-h", db_container, "-U", db_user],
        )
        if last.ok:
            return WaitResult(ok=True, waited_s=time.monotonic() - start)
        time.sleep(poll_s)
    return WaitResult(ok=False, waited_s=time.monotonic() - start, detail=last.stderr or last.stdout)


def database_exists(engine: DockerEngine, *, odoo_container: str, db_container: str, db_name: str,
                     db_user: str = "odoo", db_password: str = "odoo") -> bool:
    result = engine.exec_run(
        odoo_container,
        ["psql", "-h", db_container, "-U", db_user, "-tAc",
         f"SELECT 1 FROM pg_database WHERE datname='{db_name}'"],
    )
    return result.ok and "1" in result.stdout


def create_database(engine: DockerEngine, *, odoo_container: str, db_container: str, db_name: str,
                     db_user: str = "odoo") -> Result:
    return engine.exec_run(odoo_container, ["createdb", "-h", db_container, "-U", db_user, db_name])


def drop_database(engine: DockerEngine, *, odoo_container: str, db_container: str, db_name: str,
                   db_user: str = "odoo") -> Result:
    return engine.exec_run(
        odoo_container,
        ["dropdb", "-h", db_container, "-U", db_user, "--if-exists", db_name],
    )


def kill_odoo_process(engine: DockerEngine, *, odoo_container: str, timeout_s: float = 15.0) -> Result:
    """technical-spec.md §6.2 step 6: TERM, wait up to timeout_s, escalate to KILL."""
    term = engine.exec_run(odoo_container, ["pkill", "-TERM", "-f", "odoo-bin"])
    if not term.ok:
        # exit code 1 from pkill means "no matching process" — that's fine, nothing to kill.
        return Result(ok=True, stdout="no odoo-bin process running")
    start = time.monotonic()
    while time.monotonic() - start < timeout_s:
        still_running = engine.exec_run(odoo_container, ["pgrep", "-f", "odoo-bin"])
        if not still_running.ok:
            return Result(ok=True, stdout="terminated cleanly")
        time.sleep(0.5)
    return engine.exec_run(odoo_container, ["pkill", "-KILL", "-f", "odoo-bin"])

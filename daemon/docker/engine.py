"""DockerEngine protocol seam (implementation-brief.md §4.2): everything the daemon
needs from Docker, behind an interface with a real implementation and a fake, so unit
tests never wait on a real container. Every operation returns a structured Result
(implementation-brief.md §4.6: no bare strings, no swallowed failures).
"""

from __future__ import annotations

import subprocess
import time
from dataclasses import dataclass, field
from typing import Protocol, runtime_checkable

import docker
from docker.errors import DockerException


@dataclass(frozen=True)
class Result:
    ok: bool
    stdout: str = ""
    stderr: str = ""
    exit_code: int = 0
    duration_s: float = 0.0


@dataclass
class ContainerInfo:
    name: str
    status: str
    image: str
    labels: dict[str, str] = field(default_factory=dict)


@runtime_checkable
class DockerEngine(Protocol):
    """The subset of Docker the daemon uses. Anything not on this protocol is not
    something the daemon is allowed to do to Docker."""

    def ping(self) -> Result: ...

    def run_compose(self, compose_file: str, *args: str, cwd: str | None = None) -> Result: ...

    def list_containers(self, name_prefix: str) -> list[ContainerInfo]: ...

    def remove_container(self, name: str, force: bool = True) -> Result: ...

    def exec_run(self, container: str, cmd: list[str]) -> Result: ...

    def exec_detached(self, container: str, cmd: list[str]) -> Result:
        """Start `cmd` inside `container` and return immediately (`docker exec -d`
        semantics) — used to launch the long-running `odoo-bin` process without
        blocking on its exit (technical-spec.md §6.2 step 7)."""
        ...

    def ensure_network(self, name: str) -> Result:
        """Idempotent `docker network create` — ok even if it already exists."""
        ...


class RealDockerEngine:
    """Backed by the docker SDK for container ops, subprocess for `docker compose`
    (per implementation-brief.md §2: "the SDK's compose support is weak")."""

    def __init__(self) -> None:
        self._client = docker.from_env()

    def ping(self) -> Result:
        start = time.monotonic()
        try:
            self._client.ping()
            return Result(ok=True, duration_s=time.monotonic() - start)
        except DockerException as exc:
            return Result(ok=False, stderr=str(exc), exit_code=1, duration_s=time.monotonic() - start)

    def run_compose(self, compose_file: str, *args: str, cwd: str | None = None) -> Result:
        start = time.monotonic()
        proc = subprocess.run(
            ["docker", "compose", "-f", compose_file, *args],
            capture_output=True,
            text=True,
            cwd=cwd,
            check=False,
        )
        return Result(
            ok=proc.returncode == 0,
            stdout=proc.stdout,
            stderr=proc.stderr,
            exit_code=proc.returncode,
            duration_s=time.monotonic() - start,
        )

    def list_containers(self, name_prefix: str) -> list[ContainerInfo]:
        containers = self._client.containers.list(all=True, filters={"name": name_prefix})
        return [
            ContainerInfo(
                name=c.name,
                status=c.status,
                image=str(c.image.tags[0] if c.image.tags else c.image.id),
                labels=c.labels,
            )
            for c in containers
        ]

    def remove_container(self, name: str, force: bool = True) -> Result:
        start = time.monotonic()
        try:
            container = self._client.containers.get(name)
            container.remove(force=force)
            return Result(ok=True, duration_s=time.monotonic() - start)
        except DockerException as exc:
            return Result(ok=False, stderr=str(exc), exit_code=1, duration_s=time.monotonic() - start)

    def exec_run(self, container: str, cmd: list[str]) -> Result:
        start = time.monotonic()
        try:
            c = self._client.containers.get(container)
            exit_code, output = c.exec_run(cmd, demux=True)
            stdout_b, stderr_b = output if isinstance(output, tuple) else (output, b"")
            return Result(
                ok=exit_code == 0,
                stdout=(stdout_b or b"").decode(errors="replace"),
                stderr=(stderr_b or b"").decode(errors="replace"),
                exit_code=exit_code,
                duration_s=time.monotonic() - start,
            )
        except DockerException as exc:
            return Result(ok=False, stderr=str(exc), exit_code=1, duration_s=time.monotonic() - start)

    def exec_detached(self, container: str, cmd: list[str]) -> Result:
        start = time.monotonic()
        try:
            c = self._client.containers.get(container)
            # low-level API: high-level exec_run has no non-blocking "-d" mode.
            api = self._client.api
            exec_id = api.exec_create(c.id, cmd)["Id"]
            api.exec_start(exec_id, detach=True)
            return Result(ok=True, duration_s=time.monotonic() - start)
        except DockerException as exc:
            return Result(ok=False, stderr=str(exc), exit_code=1, duration_s=time.monotonic() - start)

    def ensure_network(self, name: str) -> Result:
        start = time.monotonic()
        try:
            existing = self._client.networks.list(names=[name])
            if not existing:
                self._client.networks.create(name, driver="bridge")
            return Result(ok=True, duration_s=time.monotonic() - start)
        except DockerException as exc:
            return Result(ok=False, stderr=str(exc), exit_code=1, duration_s=time.monotonic() - start)


class FakeDockerEngine:
    """In-memory fake for unit tests. Never touches a real Docker daemon."""

    def __init__(self) -> None:
        self.containers: dict[str, ContainerInfo] = {}
        self.compose_calls: list[tuple[str, tuple[str, ...]]] = []
        self.exec_calls: list[tuple[str, list[str]]] = []
        self.networks: set[str] = set()
        self.ping_ok = True

    def ping(self) -> Result:
        return Result(ok=self.ping_ok, exit_code=0 if self.ping_ok else 1)

    def run_compose(self, compose_file: str, *args: str, cwd: str | None = None) -> Result:
        self.compose_calls.append((compose_file, args))
        return Result(ok=True, stdout="fake compose ok")

    def list_containers(self, name_prefix: str) -> list[ContainerInfo]:
        return [c for name, c in self.containers.items() if name.startswith(name_prefix)]

    def remove_container(self, name: str, force: bool = True) -> Result:
        if name in self.containers:
            del self.containers[name]
            return Result(ok=True)
        return Result(ok=False, stderr=f"no such container: {name}", exit_code=1)

    def exec_run(self, container: str, cmd: list[str]) -> Result:
        self.exec_calls.append((container, cmd))
        return Result(ok=True, stdout="")

    def exec_detached(self, container: str, cmd: list[str]) -> Result:
        self.exec_calls.append((container, cmd))
        return Result(ok=True)

    def ensure_network(self, name: str) -> Result:
        self.networks.add(name)
        return Result(ok=True)

    def seed(self, name: str, status: str = "running", image: str = "fake:latest") -> None:
        self.containers[name] = ContainerInfo(name=name, status=status, image=image)

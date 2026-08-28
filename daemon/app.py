"""FastAPI app factory. Milestone 0: config loading, SQLite migration on startup,
and a real /health endpoint. Later milestones add the extension API, WS stream and
the MCP endpoint onto this same app (technical-spec.md §5.1: "one process").
"""

from __future__ import annotations

import sqlite3
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI
from pydantic import BaseModel

from daemon.config import TaskmanConfig
from daemon.docker.engine import DockerEngine, RealDockerEngine
from daemon.state.store import open_store


class HealthResponse(BaseModel):
    ok: bool
    db: bool
    docker: bool
    version: str = "0.1.0"


def create_app(config: TaskmanConfig | None = None, docker_engine: DockerEngine | None = None) -> FastAPI:
    cfg = config or TaskmanConfig.load()
    cfg.ensure_home()

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        app.state.config = cfg
        app.state.db = open_store(cfg.state_db_path)
        try:
            app.state.docker = docker_engine if docker_engine is not None else RealDockerEngine()
        except Exception:  # noqa: BLE001 - docker may genuinely be unavailable; health reports it
            app.state.docker = None
        yield
        app.state.db.close()

    app = FastAPI(title="taskmand", lifespan=lifespan)

    @app.get("/health", response_model=HealthResponse)
    def health() -> HealthResponse:
        db: sqlite3.Connection = app.state.db
        db_ok = False
        try:
            db.execute("SELECT 1").fetchone()
            db_ok = True
        except sqlite3.Error:
            db_ok = False

        docker_ok = False
        engine = app.state.docker
        if engine is not None:
            docker_ok = engine.ping().ok

        return HealthResponse(ok=db_ok, db=db_ok, docker=docker_ok)

    return app

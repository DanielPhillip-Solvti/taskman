"""Pydantic models mirroring the state schema (technical-spec.md §5.2). Minimal for
Milestone 0 — enough for /health and future gates to construct/validate rows against.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel

TaskState = Literal[
    "new",
    "refining",
    "refined",
    "awaiting_approval",
    "implementing",
    "implemented",
    "completing",
    "done",
    "interrupted",
]

PhaseKind = Literal["refine", "implement", "complete"]
PhaseStatus = Literal["running", "paused", "done", "stopped", "failed"]
Harness = Literal["claude", "opencode"]


class Task(BaseModel):
    id: str
    project_name: str
    task_number: int
    repo: str
    version: str
    branch: str | None = None
    state: TaskState
    title: str | None = None
    created_at: str | None = None
    updated_at: str | None = None


class LogError(BaseModel):
    """One parsed problem from an Odoo startup log (technical-spec.md §6.2 step 8)."""

    level: str  # "CRITICAL" | "ERROR" | "WARNING"
    module: str | None = None
    message: str
    file: str | None = None
    line: int | None = None
    exc_type: str | None = None  # e.g. "ParseError", "ImportError", "ValidationError"
    traceback: str | None = None


class DeployResult(BaseModel):
    """technical-spec.md §4.3."""

    ok: bool
    duration_s: float
    modules_updated: list[str] = []
    errors: list[LogError] = []
    log_tail: str = ""
    url: str = ""


class Phase(BaseModel):
    id: int | None = None
    task_id: str
    kind: PhaseKind
    harness: Harness
    session_ref: str | None = None
    status: PhaseStatus
    started_at: str | None = None
    ended_at: str | None = None

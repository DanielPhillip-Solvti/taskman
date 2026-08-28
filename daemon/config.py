"""Daemon configuration: ~/.taskman/config.yaml, loaded via pydantic-settings.

Per technical-spec.md §5.1/§3.3. Kept deliberately small for Milestone 0 — grows as
later milestones need more fields (repo mappings, harness config, sandbox image, ...).
"""

from __future__ import annotations

import os
from pathlib import Path

import yaml
from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


def default_home() -> Path:
    """~/.taskman, overridable via TASKMAN_HOME for tests/gates so nothing touches
    a real developer's home directory during verification runs."""
    override = os.environ.get("TASKMAN_HOME")
    if override:
        return Path(override)
    return Path.home() / ".taskman"


class TaskmanConfig(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="TASKMAN_", extra="ignore")

    home: Path = Field(default_factory=default_home)
    host: str = "127.0.0.1"
    port: int = 8765
    sandbox_base_image: str = "ghcr.io/solvti/odoo-env"  # see docs/DECISIONS.md for fallback
    version_roots: dict[str, str] = Field(default_factory=dict)  # e.g. {"19.0": "/code/odoo-env-19"}

    @property
    def state_db_path(self) -> Path:
        return self.home / "state.db"

    @property
    def audit_log_path(self) -> Path:
        return self.home / "audit.jsonl"

    @property
    def daemon_log_path(self) -> Path:
        return self.home / "daemon.log"

    @property
    def config_yaml_path(self) -> Path:
        return self.home / "config.yaml"

    def ensure_home(self) -> None:
        self.home.mkdir(parents=True, exist_ok=True)

    @classmethod
    def load(cls) -> "TaskmanConfig":
        """Load defaults, then overlay ~/.taskman/config.yaml if present, then env vars
        (pydantic-settings applies env vars on construction, so we pass yaml values as
        kwargs and let env still win via model_config's normal precedence for anything
        not explicitly loaded from yaml)."""
        home = default_home()
        yaml_path = home / "config.yaml"
        overrides: dict[str, object] = {}
        if yaml_path.exists():
            loaded = yaml.safe_load(yaml_path.read_text()) or {}
            if isinstance(loaded, dict):
                overrides = loaded
        return cls(**overrides)

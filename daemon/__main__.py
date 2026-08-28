from __future__ import annotations

import uvicorn

from daemon.app import create_app
from daemon.config import TaskmanConfig


def main() -> None:
    cfg = TaskmanConfig.load()
    app = create_app(cfg)
    uvicorn.run(app, host=cfg.host, port=cfg.port, log_level="info")


if __name__ == "__main__":
    main()

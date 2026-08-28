"""SQLite state store + a 20-line migration runner, per technical-spec.md §5.2 and
implementation-brief.md §2 (no ORM — raw SQL, WAL mode).
"""

from __future__ import annotations

import sqlite3
from pathlib import Path

MIGRATIONS_TABLE = """
CREATE TABLE IF NOT EXISTS _migrations (
  filename   TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (datetime('now'))
)
"""


def _migrations_dir() -> Path:
    return Path(__file__).parent / "migrations"


def connect(db_path: Path) -> sqlite3.Connection:
    db_path.parent.mkdir(parents=True, exist_ok=True)
    # check_same_thread=False: FastAPI sync endpoints run in a threadpool, not the
    # event loop thread that opened this connection during lifespan startup. Safe
    # here because access is effectively serialised by the single-operator, mostly
    # sequential nature of the daemon; revisit if that assumption changes.
    conn = sqlite3.connect(db_path, check_same_thread=False)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA foreign_keys=ON")
    conn.row_factory = sqlite3.Row
    return conn


def migrate(conn: sqlite3.Connection) -> list[str]:
    """Apply any migration file under migrations/ not yet recorded, in filename order.
    Returns the list of filenames applied (empty if already up to date)."""
    conn.execute(MIGRATIONS_TABLE)
    applied = {row[0] for row in conn.execute("SELECT filename FROM _migrations")}
    newly_applied: list[str] = []
    for path in sorted(_migrations_dir().glob("*.sql")):
        if path.name in applied:
            continue
        conn.executescript(path.read_text())
        conn.execute("INSERT INTO _migrations (filename) VALUES (?)", (path.name,))
        newly_applied.append(path.name)
    conn.commit()
    return newly_applied


def open_store(db_path: Path) -> sqlite3.Connection:
    """Connect and migrate in one step — what app startup calls."""
    conn = connect(db_path)
    migrate(conn)
    return conn


__all__ = ["connect", "migrate", "open_store"]

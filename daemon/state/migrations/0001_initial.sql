-- Initial schema, per technical-spec.md §5.2.

CREATE TABLE IF NOT EXISTS task (
  id            TEXT PRIMARY KEY,      -- "<repo>#<task_number>"
  project_name  TEXT NOT NULL,
  task_number   INTEGER NOT NULL,
  repo          TEXT NOT NULL,
  version       TEXT NOT NULL,
  branch        TEXT,
  state         TEXT NOT NULL,
  title         TEXT,
  created_at    TEXT,
  updated_at    TEXT
);

CREATE TABLE IF NOT EXISTS phase (
  id          INTEGER PRIMARY KEY,
  task_id     TEXT REFERENCES task(id),
  kind        TEXT,
  harness     TEXT,
  session_ref TEXT,
  status      TEXT,
  started_at  TEXT,
  ended_at    TEXT
);

CREATE TABLE IF NOT EXISTS report (
  id         INTEGER PRIMARY KEY,
  task_id    TEXT,
  phase_id   INTEGER,
  kind       TEXT,
  payload    JSON,
  created_at TEXT,
  applied_at TEXT
);

CREATE TABLE IF NOT EXISTS evidence (
  id         TEXT PRIMARY KEY,
  task_id    TEXT,
  path       TEXT,
  caption    TEXT,
  created_at TEXT
);

from daemon.state.store import migrate, open_store


def test_migrate_applies_once(tmp_path):
    db_path = tmp_path / "state.db"
    conn = open_store(db_path)
    tables = {
        row[0]
        for row in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")
    }
    assert {"task", "phase", "report", "evidence"} <= tables

    # Re-running migrate on an already-migrated DB applies nothing new.
    applied_again = migrate(conn)
    assert applied_again == []


def test_task_row_roundtrip(tmp_path):
    conn = open_store(tmp_path / "state.db")
    conn.execute(
        "INSERT INTO task (id, project_name, task_number, repo, version, state) "
        "VALUES (?, ?, ?, ?, ?, ?)",
        ("demo_client#1", "Demo Client", 1, "demo_client", "17.0", "new"),
    )
    conn.commit()
    row = conn.execute("SELECT * FROM task WHERE id = ?", ("demo_client#1",)).fetchone()
    assert row["repo"] == "demo_client"
    assert row["state"] == "new"

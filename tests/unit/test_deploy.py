from pathlib import Path

from daemon.docker.compose import RepoMounts
from daemon.docker.engine import FakeDockerEngine, Result
from daemon.odoo.deploy import determine_changed_modules, deploy_modules


def test_determine_changed_modules(tmp_path: Path):
    repo_dir = tmp_path / "demo_client"
    module_dir = repo_dir / "addons" / "taskman_demo"
    module_dir.mkdir(parents=True)
    (module_dir / "__manifest__.py").write_text("{'name': 'Demo'}")
    (module_dir / "models.py").write_text("# model")

    # With no git diff, changed modules list should be empty or handle cleanly
    mods = determine_changed_modules(repo_dir)
    assert isinstance(mods, list)


def test_deploy_modules_fake_engine(tmp_path: Path):
    engine = FakeDockerEngine()
    mounts = RepoMounts(
        odoo_dir=tmp_path / "odoo",
        repo_dir=tmp_path / "demo",
    )
    conf_dir = tmp_path / "conf"

    # We need exec_run on fake engine to return success for pg_isready, psql check, and cat log
    def mock_exec_run(container: str, cmd: list[str]):
        engine.exec_calls.append((container, cmd))
        cmd_str = " ".join(cmd)
        if "cat" in cmd_str:
            clean_log = (
                "2026-08-28 18:00:00,000 123 INFO db odoo.modules.loading: Modules loaded.\n"
                "2026-08-28 18:00:00,100 123 INFO db odoo.service.server: HTTP service (werkzeug) running on 0.0.0.0:8069\n"
            )
            return Result(ok=True, stdout=clean_log)
        if "psql" in cmd_str:
            return Result(ok=True, stdout="1")
        return Result(ok=True, stdout="")

    engine.exec_run = mock_exec_run

    res = deploy_modules(
        engine,
        repo="demo",
        mounts=mounts,
        conf_dir=conf_dir,
        modules=["taskman_demo"],
        poll_interval_s=0.01,
        timeout_s=1.0,
    )

    assert res.ok is True
    assert res.modules_updated == ["taskman_demo"]

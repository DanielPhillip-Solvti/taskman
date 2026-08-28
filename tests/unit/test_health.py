from fastapi.testclient import TestClient

from daemon.app import create_app
from daemon.config import TaskmanConfig
from daemon.docker.engine import FakeDockerEngine


def test_health_ok(tmp_path):
    cfg = TaskmanConfig(home=tmp_path / "taskman-home")
    app = create_app(cfg, docker_engine=FakeDockerEngine())
    with TestClient(app) as client:
        resp = client.get("/health")
        assert resp.status_code == 200
        body = resp.json()
        assert body["ok"] is True
        assert body["db"] is True
        assert body["docker"] is True


def test_health_docker_down_still_serves(tmp_path):
    cfg = TaskmanConfig(home=tmp_path / "taskman-home")
    fake = FakeDockerEngine()
    fake.ping_ok = False
    app = create_app(cfg, docker_engine=fake)
    with TestClient(app) as client:
        resp = client.get("/health")
        assert resp.status_code == 200
        assert resp.json()["docker"] is False

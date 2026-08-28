from daemon.docker.engine import FakeDockerEngine


def test_fake_engine_seed_and_list():
    engine = FakeDockerEngine()
    engine.seed("taskman-test-odoo-demo_client")
    found = engine.list_containers("taskman-test-")
    assert len(found) == 1
    assert found[0].name == "taskman-test-odoo-demo_client"


def test_fake_engine_remove_missing_is_reported_not_raised():
    engine = FakeDockerEngine()
    result = engine.remove_container("does-not-exist")
    assert result.ok is False
    assert "does-not-exist" in result.stderr


def test_fake_engine_compose_records_call():
    engine = FakeDockerEngine()
    result = engine.run_compose("docker-compose.yml", "up", "-d")
    assert result.ok is True
    assert engine.compose_calls == [("docker-compose.yml", ("up", "-d"))]

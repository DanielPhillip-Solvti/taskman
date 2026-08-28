from pathlib import Path

from daemon.docker.compose import RepoMounts, container_addons_path, render_compose, render_odoo_conf


def test_repo_mounts_addons_path():
    mounts = RepoMounts(
        odoo_dir=Path("/code/odoo"),
        repo_dir=Path("/code/demo_client"),
        enterprise_dir=Path("/code/enterprise"),
    )
    conf = render_odoo_conf(mounts, db_name="test_db", db_container="taskman-db-demo")
    assert "addons_path = /repos/odoo/addons,/repos/enterprise" in conf
    assert "db_host = taskman-db-demo" in conf
    assert "dbfilter = ^test_db$" in conf


def test_render_compose():
    mounts = RepoMounts(
        odoo_dir=Path("/code/odoo"),
        repo_dir=Path("/code/demo_client"),
    )
    yml = render_compose(
        repo="demo",
        image="ghcr.io/solvti/odoo-env:17.0",
        network="taskman-test-net",
        mounts=mounts,
        conf_host_path=Path("/tmp/odoo.conf"),
    )
    assert "taskman-odoo-demo" in yml
    assert "taskman-db-demo" in yml
    assert "taskman-test-net" in yml

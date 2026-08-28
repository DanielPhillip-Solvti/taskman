from pathlib import Path

from daemon.odoo.logparse import parse_log

FIXTURES_DIR = Path(__file__).parents[2] / "fixtures" / "odoo-logs"


def test_parse_clean_success():
    log_text = (FIXTURES_DIR / "clean_success.log").read_text()
    res = parse_log(log_text)
    assert res.ok is True
    assert "taskman_demo" in res.modules_loaded
    assert len(res.errors) == 0


def test_parse_xml_parse_error():
    log_text = (FIXTURES_DIR / "xml_parse_error.log").read_text()
    res = parse_log(log_text)
    assert res.ok is False
    assert len(res.errors) > 0
    # Check that ParseError file & line was extracted
    errs = [e for e in res.errors if e.file is not None]
    assert len(errs) > 0
    first_err = errs[0]
    assert first_err.line == 12
    assert "taskman_demo_views.xml" in first_err.file
    assert first_err.exc_type == "ParseError"


def test_parse_python_import_error():
    log_text = (FIXTURES_DIR / "python_import_error.log").read_text()
    res = parse_log(log_text)
    assert res.ok is False
    assert len(res.errors) > 0
    exc_types = [e.exc_type for e in res.errors if e.exc_type]
    assert "ModuleNotFoundError" in exc_types or "ImportError" in exc_types

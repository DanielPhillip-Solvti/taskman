"""Turns raw Odoo startup log text into structured verdicts (technical-spec.md §6.2 step 8).

"Parse errors into structured {level, module, message, traceback} rather than returning
a log blob — this is the single highest-value piece of glue in the system" — spec §6.2.

Golden-file tested against real captured boot logs in fixtures/odoo-logs/
(implementation-brief.md §4.3).
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from daemon.state.models import LogError

SUCCESS_MODULES_LOADED = re.compile(r"Modules loaded\.")
SUCCESS_HTTP_LINE = re.compile(r"HTTP service \(werkzeug\) running on|Starting HTTP")
CRITICAL_LINE = re.compile(r"^(?P<ts>\S+ \S+,\d+) (?P<pid>\d+) CRITICAL (?P<db>\S+) (?P<rest>.*)$")
ERROR_LINE = re.compile(r"^(?P<ts>\S+ \S+,\d+) (?P<pid>\d+) ERROR (?P<db>\S+) (?P<rest>.*)$")
WARNING_LINE = re.compile(r"^(?P<ts>\S+ \S+,\d+) (?P<pid>\d+) WARNING (?P<db>\S+) (?P<rest>.*)$")

# lxml's etree.XMLSyntaxError / Odoo's own convert.ParseError message shape:
#   "while parsing <path>:<line>, somewhere inside\n<xml>\nError: ..."
# or a raw lxml message "... , line N, column N"
PARSE_ERROR_FILE_LINE = re.compile(r"while parsing (?P<file>\S+):(?P<line>\d+)")
LXML_FILE_LINE = re.compile(r"(?P<file>[^\s,]+\.xml), line (?P<line>\d+)")
MODULE_FROM_PATH = re.compile(r"/addons/(?P<module>[^/]+)/")


@dataclass
class ParseResult:
    ok: bool | None  # None = still running / no terminal state observed yet
    modules_loaded: list[str]
    errors: list[LogError]
    log_tail: str


def _tail(text: str, n: int = 200) -> str:
    lines = text.splitlines()
    return "\n".join(lines[-n:])


def _extract_file_line(blob: str) -> tuple[str | None, int | None]:
    m = PARSE_ERROR_FILE_LINE.search(blob)
    if m:
        return m.group("file"), int(m.group("line"))
    m = LXML_FILE_LINE.search(blob)
    if m:
        return m.group("file"), int(m.group("line"))
    return None, None


def _extract_module(blob: str, file: str | None) -> str | None:
    for candidate in (blob, file or ""):
        m = MODULE_FROM_PATH.search(candidate)
        if m:
            return m.group("module")
    return None


def _extract_exc_type(blob: str) -> str | None:
    for name in ("ParseError", "ImportError", "ModuleNotFoundError", "ValidationError",
                 "IntegrityError", "UniqueViolation", "OperationalError", "SyntaxError"):
        if name in blob:
            return name
    return None


def parse_log(text: str) -> ParseResult:
    """Parses a full (or partial) Odoo startup log.

    ok=True  -> "Modules loaded." was reached with no CRITICAL/traceback seen.
    ok=False -> a CRITICAL line, an unhandled traceback, or a registry-build
                ParseError/ValidationError was found.
    ok=None  -> neither signal seen yet (caller should keep polling / it's a hang).
    """
    lines = text.splitlines()
    errors: list[LogError] = []
    loaded_modules: list[str] = []

    # "Loading module <name> (n/total)" lines, in order, tell us what actually ran.
    for line in lines:
        m = re.search(r"Loading module (?P<module>\S+) \(", line)
        if m:
            loaded_modules.append(m.group("module"))

    saw_critical = False
    i = 0
    n = len(lines)
    while i < n:
        line = lines[i]
        m = CRITICAL_LINE.match(line) or ERROR_LINE.match(line)
        if m:
            saw_critical = saw_critical or bool(CRITICAL_LINE.match(line))
            level = "CRITICAL" if CRITICAL_LINE.match(line) else "ERROR"
            rest = m.group("rest")
            # Gather any continuation / traceback lines until next log line starting with timestamp
            tb_lines = []
            j = i + 1
            log_line_start = re.compile(r"^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}")
            while j < n and not log_line_start.match(lines[j]):
                tb_lines.append(lines[j])
                j += 1
            traceback_text = "\n".join(tb_lines) if tb_lines else None
            blob = rest + ("\n" + traceback_text if traceback_text else "")
            file, lineno = _extract_file_line(blob)
            module = _extract_module(blob, file)
            exc_type = _extract_exc_type(blob)
            errors.append(LogError(
                level=level,
                module=module,
                message=rest.strip(),
                file=file,
                line=lineno,
                exc_type=exc_type,
                traceback=traceback_text,
            ))
            i = j
            continue
        i += 1

    ok: bool | None
    if saw_critical or any(e.level == "CRITICAL" for e in errors):
        ok = False
    elif SUCCESS_MODULES_LOADED.search(text) and SUCCESS_HTTP_LINE.search(text):
        ok = True
    else:
        ok = None

    return ParseResult(ok=ok, modules_loaded=loaded_modules, errors=errors, log_tail=_tail(text))

#!/bin/sh
# One command to start/stop/check the taskmand service, regardless of
# whether install.sh registered it with launchd (macOS) or systemd --user
# (Linux). Falls back to a plain background process if neither is set up.
#
#   taskmanctl start|stop|restart|status|logs
set -eu

LABEL="com.solvti.taskmand"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG="$HOME/.taskman/taskmand.log"
BIN="${TASKMAN_INSTALL_DIR:-$HOME/.local/bin}/taskmand"
PIDFILE="$HOME/.taskman/taskmand.pid"

usage() {
  echo "usage: taskmanctl start|stop|restart|status|logs" >&2
  exit 1
}

have_launchd() { [ "$(uname -s)" = "Darwin" ] && command -v launchctl >/dev/null 2>&1 && [ -f "$PLIST" ]; }
have_systemd() { command -v systemctl >/dev/null 2>&1 && systemctl --user status >/dev/null 2>&1 && systemctl --user cat taskmand >/dev/null 2>&1; }

[ $# -eq 1 ] || usage

case "$1" in
  start)
    if have_launchd; then launchctl load "$PLIST" 2>/dev/null || launchctl start "$LABEL"
    elif have_systemd; then systemctl --user start taskmand
    else
      mkdir -p "$HOME/.taskman"
      nohup "$BIN" >>"$LOG" 2>&1 & echo $! > "$PIDFILE"
      echo "taskman: started (pid $(cat "$PIDFILE")), logging to $LOG"
    fi
    ;;
  stop)
    if have_launchd; then launchctl unload "$PLIST"
    elif have_systemd; then systemctl --user stop taskmand
    elif [ -f "$PIDFILE" ]; then kill "$(cat "$PIDFILE")" 2>/dev/null || true; rm -f "$PIDFILE"
    else echo "taskman: nothing to stop (no service registered, no pidfile)" >&2; exit 1
    fi
    ;;
  restart)
    "$0" stop || true
    "$0" start
    ;;
  status)
    if have_launchd; then launchctl list "$LABEL" >/dev/null 2>&1 && echo "taskmand: running (launchd)" || echo "taskmand: not running"
    elif have_systemd; then systemctl --user status taskmand --no-pager
    elif [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then echo "taskmand: running (pid $(cat "$PIDFILE"))"
    else echo "taskmand: not running"
    fi
    ;;
  logs)
    if have_systemd; then journalctl --user -u taskmand -f
    else tail -f "$LOG"
    fi
    ;;
  *) usage ;;
esac

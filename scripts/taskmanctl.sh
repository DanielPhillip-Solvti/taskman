#!/bin/sh
# One command to start/stop/check/update/uninstall the taskmand service,
# regardless of whether install.sh registered it with launchd (macOS) or
# systemd --user (Linux). Falls back to a plain background process if
# neither is set up.
#
#   taskmanctl start|stop|restart|status|logs|update|uninstall
set -eu

REPO="DanielPhillip-Solvti/taskman"
LABEL="com.solvti.taskmand"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
SYSTEMD_UNIT="$HOME/.config/systemd/user/taskmand.service"
LOG="$HOME/.taskman/taskmand.log"
INSTALL_DIR="${TASKMAN_INSTALL_DIR:-$HOME/.local/bin}"
BIN="$INSTALL_DIR/taskmand"
PIDFILE="$HOME/.taskman/taskmand.pid"

usage() {
  echo "usage: taskmanctl start|stop|restart|status|logs|update|uninstall" >&2
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
  update)
    # install.sh is idempotent — re-running it fetches the latest release
    # binary and re-registers the service, overwriting what's there. It
    # doesn't restart a currently-running instance itself, so do that here
    # once the new binary is in place.
    echo "taskman: fetching latest release..."
    curl -fsSL "https://raw.githubusercontent.com/$REPO/main/scripts/install.sh" | sh
    echo "taskman: restarting with the updated binary..."
    "$0" restart
    ;;
  uninstall)
    echo "taskman: stopping service..."
    "$0" stop 2>/dev/null || true

    if [ -f "$PLIST" ]; then
      rm -f "$PLIST"
      echo "taskman: removed launchd agent ($PLIST)"
    fi
    if [ -f "$SYSTEMD_UNIT" ]; then
      systemctl --user disable taskmand >/dev/null 2>&1 || true
      rm -f "$SYSTEMD_UNIT"
      systemctl --user daemon-reload >/dev/null 2>&1 || true
      echo "taskman: removed systemd user service ($SYSTEMD_UNIT)"
    fi
    rm -f "$PIDFILE"

    rm -f "$BIN"
    echo "taskman: removed $BIN"

    # Task history/logs (~/.taskman) and the repo/config the daemon
    # manages are left in place — they're data, not part of the install,
    # and removing them silently would be a much harder mistake to notice
    # than re-running this command. Say so rather than guessing.
    echo "taskman: task history, logs, and config left at ~/.taskman — remove with: rm -rf ~/.taskman"
    echo "taskman: uninstalled. Remove the Chrome extension via chrome://extensions if you're done with it."

    # Removing the running script's own file is safe on POSIX (the open
    # fd stays valid until this process exits) — do it last so every step
    # above still has taskmanctl to report through.
    rm -f "$INSTALL_DIR/taskmanctl"
    ;;
  *) usage ;;
esac

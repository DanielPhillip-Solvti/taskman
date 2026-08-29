#!/bin/sh
# Installs the latest taskmand release to ~/.local/bin and (on macOS/Linux
# with launchd/systemd available) registers it to start on login. Safe to
# re-run — it just overwrites the binary and re-registers the service.
#
#   curl -fsSL https://raw.githubusercontent.com/DanielPhillip-Solvti/taskman/main/scripts/install.sh | sh
set -eu

REPO="DanielPhillip-Solvti/taskman"
INSTALL_DIR="${TASKMAN_INSTALL_DIR:-$HOME/.local/bin}"
BIN="$INSTALL_DIR/taskmand"

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Linux) goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) echo "taskman: unsupported OS '$os' — see https://github.com/$REPO/releases" >&2; exit 1 ;;
esac

case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) echo "taskman: unsupported architecture '$arch' — see https://github.com/$REPO/releases" >&2; exit 1 ;;
esac

asset="taskmand_${goos}_${goarch}"
url="https://github.com/$REPO/releases/latest/download/$asset"

echo "taskman: downloading $asset..."
mkdir -p "$INSTALL_DIR"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"
mv "$tmp" "$BIN"
trap - EXIT

echo "taskman: installed to $BIN"

# taskmanctl gives you one start/stop/status/logs command regardless of
# which service manager (if any) ends up registered below.
ctl_url="https://raw.githubusercontent.com/$REPO/main/scripts/taskmanctl.sh"
curl -fsSL "$ctl_url" -o "$INSTALL_DIR/taskmanctl"
chmod +x "$INSTALL_DIR/taskmanctl"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "taskman: note — $INSTALL_DIR isn't on your PATH; add it to your shell profile if you want to run 'taskmand' directly." ;;
esac

# Best-effort: register a per-user service so taskmand is always running
# in the background, the way the extension expects. Not fatal if this
# platform/setup doesn't support it — the daemon can always be run by hand.
if [ "$goos" = "darwin" ] && command -v launchctl >/dev/null 2>&1; then
  plist="$HOME/Library/LaunchAgents/com.solvti.taskmand.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.solvti.taskmand</string>
  <key>ProgramArguments</key><array><string>$BIN</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$HOME/.taskman/taskmand.log</string>
  <key>StandardErrorPath</key><string>$HOME/.taskman/taskmand.log</string>
</dict>
</plist>
EOF
  mkdir -p "$HOME/.taskman"
  launchctl unload "$plist" >/dev/null 2>&1 || true
  launchctl load "$plist"
  echo "taskman: registered as a launchd agent (com.solvti.taskmand) and started."
elif [ "$goos" = "linux" ] && command -v systemctl >/dev/null 2>&1 && systemctl --user status >/dev/null 2>&1; then
  unit_dir="$HOME/.config/systemd/user"
  mkdir -p "$unit_dir"
  cat > "$unit_dir/taskmand.service" <<EOF
[Unit]
Description=Taskman daemon

[Service]
ExecStart=$BIN
Restart=on-failure

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now taskmand
  echo "taskman: registered as a systemd user service (taskmand) and started."
else
  echo "taskman: no supported service manager found — start it yourself with: $BIN &"
fi

echo
echo "taskman: done. Manage the service with:"
echo "  $INSTALL_DIR/taskmanctl start|stop|restart|status|logs"
echo
echo "Now install the Chrome extension from"
echo "  https://github.com/$REPO#installing-the-extension"
echo "and open its toolbar icon to confirm it sees taskmand."

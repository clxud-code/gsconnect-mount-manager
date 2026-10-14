#!/bin/sh
set -eu
systemctl --user disable --now gsconnect-mount-manager.service 2>/dev/null || true
rm -f "$HOME/.config/systemd/user/gsconnect-mount-manager.service" "$HOME/.local/bin/gsconnect-mount-manager"
systemctl --user daemon-reload
echo "Removed program and service. Configuration and Android links were preserved."

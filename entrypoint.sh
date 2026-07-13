#!/bin/sh

set -eu

# The Go runtime retains the historical config directory for backward
# compatibility. A future config-version migration can move it without losing
# existing credentials or routing settings.
CONFIG_DIR="/home/appuser/.local/share/github-copilot-svcs"

mkdir -p "$CONFIG_DIR"

if [ "$(id -u)" = 0 ]; then
  chown -R appuser:appuser "$CONFIG_DIR"
  chmod 0700 "$CONFIG_DIR"
  exec su-exec appuser /app/ya-router "$@"
fi

exec /app/ya-router "$@"

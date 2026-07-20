#!/bin/sh
# install-local.sh — build ya-router from source and install it on this machine.
#
# Usage: sudo ./scripts/install-local.sh [--no-restart]
#
# Builds ya-routerd, ya-router, and ya from the working tree, installs them
# to /usr/local/bin, reloads the systemd unit, and restarts the service.
# A timestamped backup of the previous binaries is saved under /var/backups/ya-router/.
#
# Flags:
#   --no-restart   Install binaries without restarting the service.

set -eu

service=ya-router
binary_dir=/usr/local/bin
repo_dir=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
no_restart=0
rollback_needed=0
backup_dir=

for arg in "$@"; do
    case "$arg" in
        --no-restart) no_restart=1 ;;
        *) printf 'unknown flag: %s\n' "$arg" >&2; exit 2 ;;
    esac
done

fail() { printf 'install-local: %s\n' "$*" >&2; exit 1; }

restore_on_failure() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ] && [ "$rollback_needed" -eq 1 ] && [ -n "$backup_dir" ]; then
        printf 'installation failed; restoring previous binaries\n' >&2
        for binary in ya-routerd ya-router ya; do
            if [ -f "$backup_dir/$binary" ]; then
                install -m 0755 "$backup_dir/$binary" "$binary_dir/$binary" || true
            fi
        done
        if [ "$no_restart" -eq 0 ] && systemctl is-active --quiet "$service" 2>/dev/null; then
            systemctl start "$service" || true
        fi
    fi
    exit "$status"
}

[ "$(id -u)" -eq 0 ] || fail "run with sudo: sudo $0 $*"

# go is typically not in sudo's PATH; search common locations.
GO=${GO:-}
if [ -z "$GO" ]; then
    for candidate in \
        "$(command -v go 2>/dev/null)" \
        /usr/local/go/bin/go \
        /usr/go/bin/go \
        /opt/go/bin/go; do
        if [ -x "$candidate" ]; then
            GO=$candidate
            break
        fi
    done
fi
[ -n "$GO" ] || fail "go toolchain not found; set GO=/path/to/go or add it to PATH"

for cmd in install systemctl; do
    command -v "$cmd" >/dev/null 2>&1 || fail "required command not found: $cmd"
done

[ -f "$repo_dir/go.mod" ] || fail "go.mod not found in $repo_dir — run from the repo root"

printf '==> Building from %s\n' "$repo_dir"
version=$(git -C "$repo_dir" describe --tags --always --dirty 2>/dev/null || echo dev)
ldflags="-s -w -X github.com/duvu/ya-router/src.version=$version"

(cd "$repo_dir" && \
    "$GO" build -trimpath -ldflags="$ldflags" -o /tmp/ya-routerd-new ./cmd/ya-routerd && \
    "$GO" build -trimpath -ldflags="$ldflags" -o /tmp/ya-router-new  ./cmd/ya-router  && \
    "$GO" build -trimpath -ldflags="$ldflags" -o /tmp/ya-new         ./cmd/ya)

printf '==> Built binaries:\n'
ls -lh /tmp/ya-routerd-new /tmp/ya-router-new /tmp/ya-new

printf '==> Backing up current binaries\n'
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir=/var/backups/ya-router/install-$timestamp
install -d -m 0700 "$backup_dir"
for binary in ya-routerd ya-router ya; do
    [ -f "$binary_dir/$binary" ] && install -m 0755 "$binary_dir/$binary" "$backup_dir/$binary" || true
done

trap restore_on_failure EXIT HUP INT TERM
rollback_needed=1

if [ "$no_restart" -eq 0 ]; then
    printf '==> Stopping %s (systemd)\n' "$service"
    systemctl stop "$service" 2>/dev/null || true

    # Kill any ya-routerd process still holding port 7071 that was started
    # outside systemd (e.g. a manual run from a previous session).
    stray=$(ss -Htlnp 'sport = :7071' 2>/dev/null | grep -oP 'pid=\K[0-9]+' | head -1)
    if [ -n "$stray" ]; then
        printf '==> Killing stray ya-routerd on port 7071 (PID %s)\n' "$stray"
        kill "$stray" 2>/dev/null || true
        sleep 1
    fi
    # Last resort: kill any remaining ya-routerd not owned by the service.
    pkill -x ya-routerd 2>/dev/null || true
    sleep 1
fi

printf '==> Installing binaries to %s\n' "$binary_dir"
install -m 0755 /tmp/ya-routerd-new "$binary_dir/ya-routerd"
install -m 0755 /tmp/ya-router-new  "$binary_dir/ya-router"
install -m 0755 /tmp/ya-new         "$binary_dir/ya"
rm -f /tmp/ya-routerd-new /tmp/ya-router-new /tmp/ya-new

if [ "$no_restart" -eq 0 ]; then
    printf '==> Starting %s\n' "$service"
    systemctl daemon-reload
    systemctl start "$service"

    printf '==> Waiting for service to answer\n'
    attempt=0
    while [ "$attempt" -lt 15 ]; do
        if curl --silent --fail --max-time 2 http://127.0.0.1:7071/health >/dev/null 2>&1; then
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done

    systemctl --no-pager --full status "$service"
fi

rollback_needed=0
printf '==> Done. version=%s  backup=%s\n' "$version" "$backup_dir"

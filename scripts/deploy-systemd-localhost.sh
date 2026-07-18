#!/bin/sh

set -eu

service=ya-router
state_dir=/var/lib/ya-router
binary_dir=/usr/local/bin
environment_file=/etc/ya-router/ya-router.env
dropin_dir=/etc/systemd/system/ya-router.service.d
dropin_path=$dropin_dir/issue-69-logging.conf
log_file=$state_dir/logs/ya-router.log
release_dir=${1:-}
rollback_needed=0
dropin_was_present=0
backup_dir=
probe_config=
script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
. "$script_dir/deploy-systemd-localhost-lib.sh"

usage() {
	printf '%s\n' "usage: sudo $0 <verified-release-directory>" >&2
	exit 2
}

fail() {
	printf '%s\n' "deploy failed: $*" >&2
	exit 1
}

restore_on_failure() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ -n "$probe_config" ]; then
		rm -f "$probe_config"
	fi
	if [ "$status" -ne 0 ] && [ "$rollback_needed" -eq 1 ]; then
		printf '%s\n' "deployment failed; restoring the previous binaries" >&2
		for binary in ya-routerd ya-router ya; do
			if [ -f "$backup_dir/binaries/$binary" ]; then
				install -m 0755 "$backup_dir/binaries/$binary" "$binary_dir/$binary" || true
			fi
		done
		if [ "$dropin_was_present" -eq 1 ]; then
			install -m 0644 "$backup_dir/issue-69-logging.conf" "$dropin_path" || true
		else
			rm -f "$dropin_path"
		fi
		systemctl daemon-reload || true
		systemctl start "$service" || true
	fi
	exit "$status"
}

[ "$(id -u)" -eq 0 ] || fail "this script must run as root (use sudo)"
[ -n "$release_dir" ] || usage
[ -d "$release_dir" ] || fail "release directory does not exist: $release_dir"
[ -d "$state_dir" ] || fail "state directory does not exist: $state_dir"
[ -f /etc/systemd/system/ya-router.service ] || fail "systemd unit is missing: /etc/systemd/system/ya-router.service"

for command in sha256sum systemctl install tar curl grep awk chmod mktemp; do
	command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done

for artifact in ya-routerd ya-router ya checksums.txt build-info.txt; do
	[ -f "$release_dir/$artifact" ] || fail "release artifact is missing: $release_dir/$artifact"
done

for binary in ya-routerd ya-router ya; do
	[ -x "$release_dir/$binary" ] || fail "release binary is not executable: $release_dir/$binary"
	[ -x "$binary_dir/$binary" ] || fail "installed binary is missing: $binary_dir/$binary"
done

api_key=$(read_inbound_api_key "$environment_file")
if [ -n "$api_key" ]; then
	probe_config=$(mktemp)
	if ! write_curl_authorization_config "$probe_config" "$api_key"; then
		rm -f "$probe_config"
		fail "YA_ROUTER_API_KEY contains characters unsafe for curl configuration"
	fi
fi
api_key=

printf '%s\n' "Verifying release checksums from $release_dir"
(cd "$release_dir" && sha256sum -c checksums.txt)
printf '%s\n' "Release metadata:"
cat "$release_dir/build-info.txt"

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir=/var/backups/ya-router/deploy-$timestamp
install -d -m 0700 "$backup_dir/binaries"
for binary in ya-routerd ya-router ya; do
	install -m 0755 "$binary_dir/$binary" "$backup_dir/binaries/$binary"
done
if [ -f "$dropin_path" ]; then
	install -m 0644 "$dropin_path" "$backup_dir/issue-69-logging.conf"
	dropin_was_present=1
fi

trap restore_on_failure EXIT HUP INT TERM
rollback_needed=1

printf '%s\n' "Stopping $service for a consistent state backup"
systemctl stop "$service"
tar -C /var/lib -czf "$backup_dir/state.tar.gz" ya-router

printf '%s\n' "Installing verified binaries"
for binary in ya-routerd ya-router ya; do
	install -m 0755 "$release_dir/$binary" "$binary_dir/$binary"
done

install -d -m 0755 "$dropin_dir"
dropin_tmp=$(mktemp)
printf '%s\n' '[Service]' 'WorkingDirectory=/var/lib/ya-router' >"$dropin_tmp"
install -m 0644 "$dropin_tmp" "$dropin_path"
rm -f "$dropin_tmp"

systemctl daemon-reload
systemctl start "$service"

printf '%s\n' "Waiting for the localhost service"
curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 1 --max-time 5 http://127.0.0.1:7071/health
curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 1 --max-time 5 http://127.0.0.1:7071/health/ready

if [ -n "$probe_config" ]; then
	probe_status=$(curl --config "$probe_config" --silent --show-error --max-time 10 -o /dev/null -w '%{http_code}' \
		-H 'Content-Type: application/json' \
		--data '{"model":"__ya_router_logging_probe__","messages":[{"role":"user","content":"logging probe"}]}' \
		http://127.0.0.1:7071/v1/chat/completions || true)
else
	probe_status=$(curl --silent --show-error --max-time 10 -o /dev/null -w '%{http_code}' \
		-H 'Content-Type: application/json' \
		--data '{"model":"__ya_router_logging_probe__","messages":[{"role":"user","content":"logging probe"}]}' \
		http://127.0.0.1:7071/v1/chat/completions || true)
fi
case "$probe_status" in
	400) ;;
	*) fail "logging probe returned unexpected HTTP status: $probe_status" ;;
esac

attempt=0
while [ "$attempt" -lt 5 ]; do
	if [ -s "$log_file" ] && grep -F '[REQ] POST /v1/chat/completions' "$log_file" >/dev/null; then
		break
	fi
	attempt=$((attempt + 1))
	sleep 1
done
[ -s "$log_file" ] || fail "log file was not created: $log_file"
grep -F '[REQ] POST /v1/chat/completions' "$log_file" >/dev/null || fail "logging probe was not written to $log_file"

systemctl --no-pager --full status "$service"
printf '%s\n' "Deployment complete. Backup: $backup_dir"
printf '%s\n' "Verified rotating log file: $log_file"
if [ -n "$probe_config" ]; then
	rm -f "$probe_config"
	probe_config=
fi
rollback_needed=0

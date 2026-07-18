#!/bin/sh

set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
. "$script_dir/deploy-systemd-localhost-lib.sh"

environment_file=$(mktemp)
curl_config=$(mktemp)
trap 'rm -f "$environment_file" "$curl_config"' EXIT HUP INT TERM

printf '%s\n' 'YA_ROUTER_API_KEY=test-proxy-key_123' >"$environment_file"
api_key=$(read_inbound_api_key "$environment_file")
[ "$api_key" = 'test-proxy-key_123' ]

write_curl_authorization_config "$curl_config" "$api_key"
[ "$(stat -c '%a' "$curl_config")" = 600 ]
grep -Fx 'header = "Authorization: Bearer test-proxy-key_123"' "$curl_config" >/dev/null

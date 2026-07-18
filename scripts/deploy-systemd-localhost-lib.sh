#!/bin/sh

read_inbound_api_key() {
	awk '
		/^[[:space:]]*YA_ROUTER_API_KEY[[:space:]]*=/ {
			sub(/^[[:space:]]*YA_ROUTER_API_KEY[[:space:]]*=[[:space:]]*/, "")
			print
			exit
		}
	' "$1"
}

write_curl_authorization_config() {
	config_path=$1
	api_key=$2
	case "$api_key" in
		*[!A-Za-z0-9._~:/+-]*) return 1 ;;
	esac
	(umask 077; printf 'header = "Authorization: Bearer %s"\n' "$api_key" >"$config_path")
	chmod 600 "$config_path"
}

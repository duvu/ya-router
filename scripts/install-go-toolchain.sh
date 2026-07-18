#!/usr/bin/env sh

set -eu

MIN_GO_VERSION="${GO_MIN_VERSION:-1.22}"
GO_TOOLCHAIN_VERSION="${GO_TOOLCHAIN_VERSION:-}"
GO_ROOT_DIR="${GO_ROOT_DIR:-}"
FORCE_INSTALL="${FORCE_INSTALL:-0}"
AUTO_PROFILE="${AUTO_PROFILE:-0}"

usage() {
	cat <<'USAGE'
Usage: install-go-toolchain.sh [options]

Install a Go toolchain suitable for ya-router development.

Options:
  --version VERSION     Specific Go version to install (for example 1.22.9)
  --install-dir DIR     Base directory where Go will be installed (default: /usr/local for root, $HOME/.local otherwise)
  --force               Force reinstall even if a suitable version is already found
  --auto-profile        Add PATH export to your shell rc file automatically
  -h, --help            Show this message

Environment variables:
  GO_MIN_VERSION        Minimum supported version (default: 1.22)
  GO_TOOLCHAIN_VERSION  Override version to install
  GO_ROOT_DIR           Directory containing extracted Go toolchain
  FORCE_INSTALL         Set to 1 to force install
  AUTO_PROFILE          Set to 1 to enable automatic profile update
USAGE
}

version_ge() {
	[ "$(printf "%s\n%s\n" "$2" "$1" | sort -V | head -n 1)" = "$2" ]
}

go_version() {
	go_binary=$1
	if [ -z "$go_binary" ] || [ ! -x "$go_binary" ]; then
		return 1
	fi
	"$go_binary" version 2>/dev/null | awk '{print $3}' | sed 's/^go//'
}

check_existing_version() {
	go_binary="${1:-go}"
	ver=$(go_version "$go_binary" || true)
	if [ -n "$ver" ]; then
		printf '%s\n' "$ver"
		return 0
	fi
	return 1
}

platform_from_uname() {
	case "$(uname -s)" in
		Linux) printf '%s\n' linux ;;
		Darwin) printf '%s\n' darwin ;;
		*)
			echo "unsupported OS: $(uname -s)" >&2
			return 1
			;;
	esac
}

arch_from_uname() {
	case "$(uname -m)" in
		x86_64|amd64) printf '%s\n' amd64 ;;
		aarch64|arm64) printf '%s\n' arm64 ;;
		armv6l) printf '%s\n' armv6l ;;
		armv7l) printf '%s\n' armv7l ;;
		i386|i686) printf '%s\n' 386 ;;
		*)
			echo "unsupported architecture: $(uname -m)" >&2
			return 1
			;;
	esac
}

latest_go_version() {
	json=$(curl -fsSL "https://go.dev/dl/?mode=json")
	echo "$json" | awk -F'"' 'BEGIN{RS=","} /"version":/ {print $4; exit}'
}

need_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required command missing: $1" >&2
		exit 1
	fi
}

parse_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
			--version)
				if [ $# -lt 2 ]; then
					echo "missing argument: --version VERSION" >&2
					usage
					exit 1
				fi
				GO_TOOLCHAIN_VERSION="${2:-}"
				shift 2
				;;
			--install-dir)
				if [ $# -lt 2 ]; then
					echo "missing argument: --install-dir DIR" >&2
					usage
					exit 1
				fi
				GO_ROOT_DIR="${2:-}"
				shift 2
				;;
			--force)
				FORCE_INSTALL=1
				shift
				;;
			--auto-profile)
				AUTO_PROFILE=1
				shift
				;;
			-h|--help)
				usage
				exit 0
				;;
			*)
				echo "unknown option: $1" >&2
				usage
				exit 1
				;;
		esac
	done
}

set_profile_path() {
	if [ "$AUTO_PROFILE" -ne 1 ]; then
		return 0
	fi

	shell_name="${SHELL##*/}"
	case "$shell_name" in
		bash) profile="$HOME/.bashrc" ;;
		zsh) profile="$HOME/.zshrc" ;;
		fish)
			profile="$HOME/.config/fish/config.fish"
			;;
		*)
			echo "skip auto-profile: unsupported shell $shell_name"
			return 0
			;;
	esac

	[ -n "${profile:-}" ] || return 0
	mkdir -p "$(dirname "$profile")"
	case "$shell_name" in
		fish)
			line="set -gx PATH $GO_INSTALL_PATH $PATH"
			;;
		*)
			line="export PATH=\"$GO_INSTALL_PATH:\$PATH\""
			;;
	esac

	if [ -f "$profile" ] && grep -F -q "$GO_INSTALL_PATH" "$profile"; then
		return 0
	fi
	echo '' >> "$profile"
	echo '# Added for ya-router Go toolchain' >> "$profile"
	echo "$line" >> "$profile"
	echo "Updated $profile for PATH."
}

parse_args "$@"

GO_TOOLCHAIN_VERSION="${GO_TOOLCHAIN_VERSION#go}"
GO_ROOT_DIR="${GO_ROOT_DIR%/}"

need_cmd awk
need_cmd sed
need_cmd sort
need_cmd tar
need_cmd mktemp

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
	echo "either curl or wget is required" >&2
	exit 1
fi

current_version="$(check_existing_version go || true)"
if [ -n "$current_version" ] && version_ge "$current_version" "$MIN_GO_VERSION" && [ "$FORCE_INSTALL" -eq 0 ]; then
	echo "Go $current_version is already installed and satisfies minimum version $MIN_GO_VERSION."
	echo "No action needed."
	exit 0
fi

platform=$(platform_from_uname)
arch=$(arch_from_uname)

if [ -n "$GO_TOOLCHAIN_VERSION" ]; then
	target_version="$GO_TOOLCHAIN_VERSION"
else
	target_version="$(latest_go_version | sed 's/^go//')"
fi

if [ -z "$target_version" ] || ! version_ge "$target_version" "$MIN_GO_VERSION"; then
	echo "cannot determine a Go version >= $MIN_GO_VERSION" >&2
	exit 1
fi

if [ -z "$GO_ROOT_DIR" ]; then
	if [ "$(id -u)" -eq 0 ]; then
		GO_ROOT_DIR="/usr/local/go"
	else
		GO_ROOT_DIR="$HOME/.local/go"
	fi
fi

archive="go${target_version}.${platform}-${arch}.tar.gz"
url="https://go.dev/dl/$archive"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$url" -o "$work_dir/$archive"
else
	wget -qO "$work_dir/$archive" "$url"
fi

tar -C "$work_dir" -xzf "$work_dir/$archive"
mkdir -p "$(dirname "$GO_ROOT_DIR")"
rm -rf "$GO_ROOT_DIR"
mv "$work_dir/go" "$GO_ROOT_DIR"

installed_version="$(check_existing_version "$GO_ROOT_DIR/bin/go" || true)"
if [ -z "$installed_version" ]; then
	echo "Go install validation failed: cannot execute $GO_ROOT_DIR/bin/go" >&2
	exit 1
fi
if ! version_ge "$installed_version" "$MIN_GO_VERSION"; then
	echo "Installed Go version $installed_version is below minimum $MIN_GO_VERSION" >&2
	exit 1
fi

echo "Go $installed_version installed to $GO_ROOT_DIR"

GO_INSTALL_PATH="$GO_ROOT_DIR/bin"
if echo "$PATH" | awk -v dir="$GO_INSTALL_PATH" -F: '{for(i=1;i<=NF;i++) if($i==dir){found=1; exit}} END{exit !found}'; then
	echo "Go bin directory is already on PATH."
else
	echo "Add this to your shell before building:"
	echo "  export PATH=\"$GO_INSTALL_PATH:\$PATH\""
	if [ "$AUTO_PROFILE" -eq 1 ]; then
		set_profile_path
	fi
fi

echo "Done. Current go version:"
"$GO_ROOT_DIR/bin/go" version

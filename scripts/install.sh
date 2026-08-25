#!/bin/sh
# Install WUT.
#
# The whole point of this script is the checksum. A one-line installer that
# pipes a download into a shell and never verifies it has taught the user to
# trust whatever answers that hostname today; this one refuses to unpack an
# archive whose SHA-256 does not appear in the signed checksums file.
#
#   curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | sh
#
# Environment:
#   WUT_VERSION   a tag to install, e.g. v1.0.0 (default: the latest release)
#   WUT_INSTALL   where the binary goes (default: ~/.local/bin)
#   WUT_SKIP_CHECKSUM=1   skip verification. Only for a machine with no
#                         sha256 tool at all, and it says so loudly.

set -eu

REPO="thirawat27/wut"
INSTALL_DIR="${WUT_INSTALL:-$HOME/.local/bin}"

say()  { printf '  %s\n' "$*"; }
warn() { printf '  warning: %s\n' "$*" >&2; }
die()  { printf '  error: %s\n' "$*" >&2; exit 1; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "this script needs $1"
}

# --- what are we running on -------------------------------------------------

detect_platform() {
	os=$(uname -s)
	case "$os" in
		Linux)   os=linux ;;
		Darwin)  os=darwin ;;
		FreeBSD) os=freebsd ;;
		OpenBSD) os=openbsd ;;
		NetBSD)  os=netbsd ;;
		MINGW*|MSYS*|CYGWIN*)
			die "on Windows use scripts/install.ps1 instead" ;;
		*) die "unsupported operating system: $os" ;;
	esac

	arch=$(uname -m)
	case "$arch" in
		x86_64|amd64) arch=amd64 ;;
		arm64|aarch64) arch=arm64 ;;
		*) die "unsupported architecture: $arch" ;;
	esac

	printf '%s %s' "$os" "$arch"
}

# --- download helpers -------------------------------------------------------

fetch() {
	# fetch <url> <destination>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --connect-timeout 20 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "this script needs curl or wget"
	fi
}

fetch_stdout() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --connect-timeout 20 "$1"
	else
		wget -qO- "$1"
	fi
}

latest_version() {
	fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' |
		head -n 1
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v sha256 >/dev/null 2>&1; then
		sha256 -q "$1"
	else
		return 1
	fi
}

# --- verify -----------------------------------------------------------------

verify() {
	# verify <archive> <checksums-file> <archive-name>
	expected=$(grep " \*\{0,1\}$3\$" "$2" | cut -d' ' -f1 | head -n 1)
	[ -n "$expected" ] || die "$3 is not listed in checksums.txt"

	if ! actual=$(sha256_of "$1"); then
		if [ "${WUT_SKIP_CHECKSUM:-0}" = "1" ]; then
			warn "no sha256 tool found and WUT_SKIP_CHECKSUM=1 — installing UNVERIFIED"
			return 0
		fi
		die "no sha256 tool found. Install one, or re-run with WUT_SKIP_CHECKSUM=1 to accept an unverified binary"
	fi

	if [ "$actual" != "$expected" ]; then
		printf '  expected %s\n  actual   %s\n' "$expected" "$actual" >&2
		die "checksum mismatch — refusing to install"
	fi
	say "checksum ok"
}

# --- main -------------------------------------------------------------------

main() {
	need uname
	need tar

	set -- $(detect_platform)
	os=$1; arch=$2

	version="${WUT_VERSION:-}"
	if [ -z "$version" ]; then
		version=$(latest_version)
		[ -n "$version" ] || die "could not determine the latest version; set WUT_VERSION"
	fi
	# The archive name has no leading v; the tag does.
	bare=${version#v}

	archive="wut_${bare}_${os}_${arch}.tar.gz"
	base="https://github.com/$REPO/releases/download/$version"

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t wut)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "wut $version for $os/$arch"
	fetch "$base/$archive" "$tmp/$archive" || die "could not download $archive"
	fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "could not download checksums.txt"
	verify "$tmp/$archive" "$tmp/checksums.txt" "$archive"

	tar -xzf "$tmp/$archive" -C "$tmp"
	[ -f "$tmp/wut" ] || die "the archive did not contain a wut binary"

	mkdir -p "$INSTALL_DIR"
	# Install to a temporary name and rename, so a running wut is never
	# half-overwritten.
	cp "$tmp/wut" "$INSTALL_DIR/.wut.new"
	chmod 0755 "$INSTALL_DIR/.wut.new"
	mv "$INSTALL_DIR/.wut.new" "$INSTALL_DIR/wut"

	say "installed $INSTALL_DIR/wut"

	case ":$PATH:" in
		*":$INSTALL_DIR:"*) ;;
		*) warn "$INSTALL_DIR is not on your PATH. Add it, then re-open your shell." ;;
	esac

	printf '\n  Next:\n    wut shell install    %s\n    wut db sync          %s\n\n' \
		"# so bare 'wut' knows what just failed" \
		"# build the local knowledge index"
}

main "$@"

#!/bin/sh
# install.sh downloads the newest release of clawdiator for this computer, checks it and runs "clawdiator install".
# Usage: curl -fsSL https://github.com/keyCat/clawdiator/releases/latest/download/install.sh | sh
# The options of install come after "sh -s --", for example: curl … | sh -s -- --blocked-countries AF
# CLAWDIATOR_RELEASE_URL selects a different folder with the release files. The tests use it.
set -eu

release_url="${CLAWDIATOR_RELEASE_URL:-https://github.com/keyCat/clawdiator/releases/latest/download}"

fail() {
	echo "error: $*" >&2
	exit 1
}

# fetch downloads the address $1 to the file $2. Ubuntu Desktop can have wget without curl.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		fail "install curl or wget first"
	fi
}

case "$(uname -s)" in
Darwin) system=darwin ;;
Linux) system=linux ;;
*) fail "clawdiator works only on macOS and Linux" ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) fail "clawdiator has no version for the processor $(uname -m)" ;;
esac
# A shell under Rosetta shows x86_64 also on a Mac with Apple silicon.
if [ "$system" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi

archive="clawdiator-$system-$arch.tar.gz"
folder=$(mktemp -d)
trap 'rm -rf "$folder"' EXIT
cd "$folder"

echo "Download $archive."
fetch "$release_url/$archive" "$archive" || fail "the download of $archive failed"
fetch "$release_url/SHA256SUMS" SHA256SUMS || fail "the download of SHA256SUMS failed"
line=$(grep " $archive\$" SHA256SUMS) || fail "SHA256SUMS has no line for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	printf '%s\n' "$line" | sha256sum -c - >/dev/null 2>&1 || fail "the checksum of $archive is not correct"
else
	printf '%s\n' "$line" | shasum -a 256 -c - >/dev/null 2>&1 || fail "the checksum of $archive is not correct"
fi
tar -xzf "$archive"

set -- install "$@"
if [ "$(id -u)" != 0 ]; then
	echo "Run \"sudo clawdiator install\". Type your password."
	set -- sudo ./clawdiator "$@"
else
	set -- ./clawdiator "$@"
fi
# With "curl | sh", the input of this script is the script itself. The question about the consent needs the terminal.
if [ ! -t 0 ] && (: </dev/tty) 2>/dev/null; then
	"$@" </dev/tty
else
	"$@"
fi

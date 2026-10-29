#!/bin/sh
# release-notes.sh prints the section of CHANGELOG.md for a version.
# Usage: scripts/release-notes.sh v0.1.1
# The workflow "Release" uses the output as the text of the release. The script fails if the section is missing or empty.
set -eu

version="${1:-}"
if [ -z "$version" ]; then
	echo "Give the version as the first argument, for example v0.1.1." >&2
	exit 2
fi
root=$(cd "$(dirname "$0")/.." && pwd)
# The heading of a section is "## v0.1.1 - 2026-10-29". The empty lines before the first text line are not printed.
notes=$(awk -v version="$version" '
	/^## / { if (isFound) exit; isFound = ($2 == version); next }
	isFound && (NF || hasText) { hasText = 1; print }
' "$root/CHANGELOG.md")
if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
	echo "CHANGELOG.md has no text for $version. Write the changes of the version first." >&2
	exit 1
fi
printf '%s\n' "$notes"
# Each release has the same 2 archives, so each release text tells which archive fits which Mac.
cat <<'NOTES'

### Downloads

- `clawdiator-darwin-arm64.tar.gz` is for a Mac with Apple silicon.
- `clawdiator-darwin-amd64.tar.gz` is for a Mac with an Intel processor. It is not tested.

The binaries have no signature of Apple. Before the installation, read the steps in the [README](https://github.com/keyCat/clawdiator#install).
Compare the checksum of the archive with `SHA256SUMS`.
NOTES

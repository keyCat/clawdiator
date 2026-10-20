#!/bin/sh
# build-release.sh builds the release archives of clawdiator for macOS and checks them.
# Usage: scripts/build-release.sh v0.1.0
# The result is in dist/release: 1 archive for each architecture, and SHA256SUMS.
# The script fails if 1 check fails. The GitHub workflow "Release" runs the same script.
set -eu
# The C locale gives the same order of sort on each computer.
export LC_ALL=C

version="${1:-}"
case "$version" in
v[0-9]*) ;;
*)
	echo "Give the version as the first argument, for example v0.1.0." >&2
	exit 2
	;;
esac

# Go 1.26 is the last Go that runs on macOS 12 Monterey. Go 1.27 needs macOS 13.
# So the release uses Go 1.26, and the tray gets the same minimum macOS version.
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.10}"
export MACOSX_DEPLOYMENT_TARGET=12.0
case "$(go version)" in
*" go1.26."*) ;;
*)
	echo "The release must use Go 1.26, but the build uses: $(go version)" >&2
	exit 1
	;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/dist/release"
rm -rf "$out"
mkdir -p "$out"
cd "$root"

fail() {
	echo "CHECK FAILED: $*" >&2
	exit 1
}

# write_licenses writes the license texts of Go and of each module in the binaries.
# go version -m lists the modules that are really in a binary. The modules of the tests are not in it.
write_licenses() {
	file="$1"
	shift
	{
		echo "The binaries of clawdiator contain the code of Go and of these modules."
		echo "This file gives the license text of each of them."
		echo
		echo "=== Go $(go env GOVERSION)"
		cat "$(go env GOROOT)/LICENSE"
	} >"$file"
	for module in $(go version -m "$@" | awk '$1 == "dep" { print $2 "@" $3 }' | sort -u); do
		dir=$(go mod download -json "$module" | awk -F'"' '/"Dir":/ { print $4 }')
		licenses=$(find "$dir" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) | sort)
		[ -n "$licenses" ] || fail "the module $module has no license file"
		for license in $licenses; do
			printf '\n=== %s (%s)\n' "$module" "$(basename "$license")" >>"$file"
			cat "$license" >>"$file"
		done
	done
}

for arch in arm64 amd64; do
	stage="$out/clawdiator-darwin-$arch"
	mkdir -p "$stage"
	ldflags="-s -w -X main.version=$version"
	# The main binary has no cgo. A root service must not link the Cocoa framework of the tray.
	CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$stage/clawdiator" .
	CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" go build -trimpath -tags tray -ldflags "$ldflags" -o "$stage/clawdiator-tray" .

	machine=$arch
	[ "$arch" = amd64 ] && machine=x86_64
	for binary in clawdiator clawdiator-tray; do
		path="$stage/$binary"
		[ "$(lipo -archs "$path")" = "$machine" ] || fail "$binary of $arch has the architecture $(lipo -archs "$path")"
		minos=$(otool -l "$path" | awk '/LC_BUILD_VERSION/ { found = 1 } found && $1 == "minos" { print $2; exit }')
		[ "$minos" = "$MACOSX_DEPLOYMENT_TARGET" ] || fail "$binary of $arch needs macOS $minos, not $MACOSX_DEPLOYMENT_TARGET"
		# Apple silicon starts only a binary with a signature. The linker gives an ad-hoc signature.
		if [ "$arch" = arm64 ]; then
			codesign -v "$path" 2>/dev/null || fail "$binary of $arch has no correct signature"
		fi
	done
	if otool -L "$stage/clawdiator" | grep -qE 'Cocoa|AppKit'; then
		fail "the main binary of $arch links Cocoa or AppKit"
	fi
	if [ "$(uname -m)" = "$machine" ]; then
		[ "$("$stage/clawdiator" version)" = "clawdiator $version" ] || fail "the main binary of $arch shows the version $("$stage/clawdiator" version)"
	fi

	cp README.md LICENSE "$stage/"
	write_licenses "$stage/THIRD_PARTY_LICENSES.txt" "$stage/clawdiator" "$stage/clawdiator-tray"
	archive="$out/clawdiator-darwin-$arch.tar.gz"
	# Without these 2 settings, bsdtar adds files with the extended attributes of macOS, for example ._clawdiator.
	(cd "$stage" && COPYFILE_DISABLE=1 tar --no-mac-metadata -czf "$archive" clawdiator clawdiator-tray README.md LICENSE THIRD_PARTY_LICENSES.txt)
	listed=$(tar -tzf "$archive" | sort | tr '\n' ' ')
	[ "$listed" = "LICENSE README.md THIRD_PARTY_LICENSES.txt clawdiator clawdiator-tray " ] || fail "the archive of $arch contains: $listed"
	rm -rf "$stage"
done

(cd "$out" && shasum -a 256 clawdiator-darwin-*.tar.gz >SHA256SUMS)
echo "The release $version is in $out:"
ls -l "$out"

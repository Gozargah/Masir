#!/bin/sh
# Builds Linux desktop packages signed with a throwaway key, laid out like a GitHub
# release, for testing the packaged-app update on this machine.
#
#   sh scripts/build-test-packages.sh dist/test-packages 0.1.90 0.1.91
#
# Each version goes to <out>/v<version>/: the .deb, checksums-desktop.txt and its
# .minisig (MyGo makes the update manifest and the portable tarball only when
# MYGO_UPDATER_PRIVATE_KEY is set; the package update needs neither). Serve <out> on
# 127.0.0.1 and point MASIR_DESKTOP_RELEASE_BASE at it. The key is made once into <out>/test-key.env (mode
# 600); it is a test key, never commit it. The masir in these packages trusts only it.
#
# This calls build-gui.sh, not `make gui-local`: the Makefile includes .env, whose release
# keys would replace the test key.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

[ $# -ge 2 ] || { echo "usage: $0 <out-dir> <version> [<version>...]" >&2; exit 2; }
mkdir -p "$1"
out=$(cd "$1" && pwd)
shift
arch=$(go env GOARCH)

key="$out/test-key.env"
if [ ! -f "$key" ]; then
	(umask 077 && go run ./libs/masir-daemon/cmd/test-sign keygen >"$key.tmp" && mv "$key.tmp" "$key")
fi
. "$key"
export MASIR_UPDATE_PUBKEY MINISIGN_SECRET_KEY

for v in "$@"; do
	# build-gui.sh builds GUI_VERSION without touching mygo.json, as the release does.
	GUI_VERSION=$v GUI_OS=linux GUI_ARCHES=$arch MASIR_SIGN_COMPONENTS=1 sh scripts/build-gui.sh

	dir="$out/v$v"
	rm -rf "$dir"
	mkdir -p "$dir"
	cp apps/masir-gui/dist/linux-"$arch"/masir-desktop-*"-$v-"* "$dir/"
	cp apps/masir-gui/dist/linux-"$arch"/update-*.json "$dir/" 2>/dev/null || true
	# The list release.yml signs: every download, sorted by name.
	(cd "$dir" && sha256sum masir-desktop-* $(ls update-* 2>/dev/null) | sort -k2 >checksums-desktop.txt)
	go run ./libs/masir-daemon/cmd/test-sign sign "$dir/checksums-desktop.txt"
	printf '==> %s\n' "$dir"
	ls "$dir"
done

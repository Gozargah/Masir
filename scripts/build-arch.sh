#!/bin/sh
# Generate release packaging after both Linux debs have been built.
# Requires makepkg (Arch Linux); no package compilation or downloads occur.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=${1:?usage: build-arch.sh VERSION OUTPUT_DIRECTORY}
out=${2:?usage: build-arch.sh VERSION OUTPUT_DIRECTORY}
case "$version" in
    ''|*[!0-9.]*|.*|*.) echo 'build-arch: expected a numeric release version' >&2; exit 1 ;;
esac
command -v makepkg >/dev/null
mkdir -p "$out"
sum() {
    sha256sum "$root/apps/masir-gui/dist/linux-$1/masir-desktop-linux-$version-$1.deb" | cut -d ' ' -f1
}
amd64=$(sum amd64)
arm64=$(sum arm64)
sed -e "s/@VERSION@/$version/g" \
    -e "s/@AMD64_SHA256@/$amd64/g" \
    -e "s/@ARM64_SHA256@/$arm64/g" \
    "$root/packaging/PKGBUILD.in" > "$out/PKGBUILD"
(cd "$out" && makepkg --printsrcinfo > .SRCINFO)

#!/bin/sh
# Run a command with the pinned Xray source patched in a private workspace.
# Like the MyGo build, patch a downloaded copy, never the module cache. Unlike
# temporary go.mod edits, the workspace also permits independent concurrent runs.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
[ "$#" -gt 0 ] || { echo 'usage: with-xray.sh command [args...]' >&2; exit 2; }
# Explicit command flags take precedence over GOFLAGS. Reject a direct Go tag
# override that would silently disable the patched-only probe implementation.
require_patch_tag() {
 case ",$(printf '%s' "$1" | tr ' ' ',')," in
  *,xray_patched,*) ;;
  *) echo 'with-xray: explicit -tags must include xray_patched' >&2; exit 2 ;;
 esac
}
case "${1##*/}" in
 go|go.exe)
  tag_value=no
  for arg in "$@"; do
   if [ "$tag_value" = yes ]; then require_patch_tag "$arg"; tag_value=no; continue; fi
   case "$arg" in
    -args|--) break ;;
    -tags|--tags) tag_value=yes ;;
    -tags=*|--tags=*) require_patch_tag "${arg#*=}" ;;
   esac
  done
  [ "$tag_value" = no ] || { echo 'with-xray: missing -tags value' >&2; exit 2; }
 ;;
esac
# Preserve existing comma-separated tag lists as well as other Go flags.
patch_tags=xray_patched
set -f
for flag in ${GOFLAGS:-}; do
 case "$flag" in
  -tags=*|--tags=*) patch_tags="${flag#*=},xray_patched" ;;
 esac
done
set +f
GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=$patch_tags"
export GOFLAGS
if [ -n "${MASIR_XRAY_SOURCE:-}" ] && [ -f "$MASIR_XRAY_SOURCE/go.mod" ]; then
 exec "$@"
fi
# Keep preparation and standalone dependency tests on the repository toolchain.
GOTOOLCHAIN=${GOTOOLCHAIN:-$(cd "$root" && go env GOVERSION)+auto}
export GOTOOLCHAIN
work=$(mktemp -d "$root/.xray-patched.XXXXXX")
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Resolve the repository's pinned version, without an inherited workspace override.
xray_dir=$(cd "$root/libs/masir-daemon" && GOWORK=off go mod download github.com/xtls/xray-core && GOWORK=off go list -m -f '{{.Dir}}' github.com/xtls/xray-core)
[ -d "$xray_dir" ] || { echo 'with-xray: cannot find Xray source' >&2; exit 1; }
cp -R "$xray_dir" "$work/module"
chmod -R u+w "$work/module"
for patch in "$root"/patches/xray-*.patch; do
 (cd "$root" && git apply --directory="$(basename "$work")/module" --whitespace=nowarn "$patch") || {
  echo "with-xray: $(basename "$patch") does not apply to the pinned Xray version" >&2
  exit 1
 }
done
[ -f "$work/module/transport/internet/finalmask/xicmp/client_control_test.go" ] || {
 echo 'with-xray: XICMP patch was not applied' >&2
 exit 1
}
# Go emits native paths here, including on Windows under Git Bash.
(cd "$work" && GOWORK=off go work init "$root/apps/masir" "$root/apps/masir-gui" "$root/libs/masir-core" "$root/libs/masir-daemon" "$root/libs/masir-proto")
GOWORK=$(cd "$work" && GOWORK=auto go env GOWORK)
# MSYS dirname needs slash separators even when Go reports native Windows paths.
if [ "$(go env GOHOSTOS)" = windows ]; then
 GOWORK=$(printf '%s' "$GOWORK" | tr '\\' '/')
fi
export GOWORK
native_work=$(dirname "$GOWORK")
go work edit -replace="github.com/xtls/xray-core=$native_work/module"
MASIR_XRAY_SOURCE="$native_work/module"
export MASIR_XRAY_SOURCE
"$@"

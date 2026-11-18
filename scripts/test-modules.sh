#!/bin/sh
# The same project and patched-dependency checks locally and in CI.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -z "${MASIR_XRAY_SOURCE:-}" ]; then
 exec sh "$root/scripts/with-xray.sh" sh "$root/scripts/test-modules.sh" "$@"
fi
sh "$root/scripts/test-xray-preparation.sh"
for module in apps/masir apps/masir-gui libs/masir-daemon libs/masir-core libs/masir-proto; do
 printf '==> %s\n' "$module"
 (cd "$root/$module" && go vet ./... && go test "$@" ./...)
done
(cd "$MASIR_XRAY_SOURCE" && GOWORK=off go test "$@" ./transport/internet/finalmask/xicmp ./proxy/tun)

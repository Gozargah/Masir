#!/bin/sh
# Let Go parse user flags itself; dependency capability must not depend on tags.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
for flags in '-tags=desktop' "'-tags=desktop'" "'-tags=desktop extra'"; do
 actual=$(GOFLAGS="$flags" sh scripts/with-xray.sh go env GOFLAGS)
 [ "$actual" = "$flags" ] || { echo 'GOFLAGS was changed' >&2; exit 1; }
 files=$(GOOS=darwin GOFLAGS="$flags" sh scripts/with-xray.sh go list -f '{{join .GoFiles " "}}' ./libs/masir-daemon/internal/xray)
 case "$files" in *probe_interface_darwin.go*) ;; *) echo 'tags disabled the Darwin binding helper' >&2; exit 1 ;; esac
 if [ "$(go env GOHOSTOS)" = darwin ]; then
  GOFLAGS="$flags" sh scripts/with-xray.sh go test -run '^TestPreparedProbeBindingAvailable$' ./libs/masir-daemon/internal/xray
 fi
done
# Exercise Go's comma, whitespace, and mixed-list validation paths.
for tags in 'desktop,xray_patched' 'desktop xray_patched' 'desktop,xray_patched extra'; do
 # Compare the executable test command: Go list and Go test/vet can reject
 # different forms. The wrapper must preserve Go's own validation behavior.
 if native=$(go test "-tags=$tags" -run '^TestPreparedProbeBindingAvailable$' ./libs/masir-daemon/internal/xray 2>&1); then
  sh scripts/with-xray.sh go test "-tags=$tags" -run '^TestPreparedProbeBindingAvailable$' ./libs/masir-daemon/internal/xray
  files=$(GOOS=darwin sh scripts/with-xray.sh go list "-tags=$tags" -f '{{join .GoFiles " "}}' ./libs/masir-daemon/internal/xray)
  case "$files" in *probe_interface_darwin.go*) ;; *) echo 'explicit tags disabled the helper' >&2; exit 1 ;; esac
 else
  expected=$(printf '%s\n' "$native" | sed -n '/^go: -tags/p')
  [ -n "$expected" ] || { echo 'native Go test failed unexpectedly' >&2; exit 1; }
  if wrapped=$(sh scripts/with-xray.sh go test "-tags=$tags" -run '^TestPreparedProbeBindingAvailable$' ./libs/masir-daemon/internal/xray 2>&1); then
   echo 'wrapper accepted flags rejected by Go' >&2; exit 1
  fi
  actual=$(printf '%s\n' "$wrapped" | sed -n '/^go: -tags/p')
  [ "$actual" = "$expected" ] || { echo 'wrapper changed Go tag validation' >&2; exit 1; }
 fi
done

#!/bin/sh
# Guard against direct Go flags silently selecting the stock probe helper.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
set +e
output=$(sh scripts/with-xray.sh go list -tags=desktop ./libs/masir-daemon/internal/xray 2>&1)
status=$?
set -e
[ "$status" = 2 ] || { echo 'unsafe tag override did not return the expected rejection' >&2; exit 1; }
case "$output" in *'explicit -tags must include xray_patched'*) ;; *) echo 'unexpected tag rejection' >&2; exit 1 ;; esac
# Cross-list Darwin so these checks exercise its conditional helper on every OS.
files=$(GOOS=darwin GOFLAGS=-tags=desktop sh scripts/with-xray.sh go list -f '{{join .GoFiles " "}}' ./libs/masir-daemon/internal/xray)
case "$files" in *probe_interface_darwin.go*) ;; *) echo 'inherited tags lost the patched helper' >&2; exit 1 ;; esac
files=$(GOOS=darwin sh scripts/with-xray.sh go list -tags=desktop,xray_patched -f '{{join .GoFiles " "}}' ./libs/masir-daemon/internal/xray)
case "$files" in *probe_interface_darwin.go*) ;; *) echo 'explicit safe tags lost the patched helper' >&2; exit 1 ;; esac

#!/bin/sh
# Runs the desktop app from source against a patched MyGo, for the fast loop when the
# change depends on a patch in patches/. scripts/build-gui.sh does the same for the
# packaged apps; the headless tests and go vet still use the stock module.
#
# Ctrl+C stops the app; go.mod and go.sum are put back and the patched copy removed.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

work="$root/.mygo-patched"
go_mod="$root/apps/masir-gui/go.mod"
go_sum="$root/apps/masir-gui/go.sum"
go_mod_orig="$root/.mygo-patched.go.mod"
go_sum_orig="$root/.mygo-patched.go.sum"

cleanup() {
	if [ -f "$go_mod_orig" ]; then
		cp "$go_mod_orig" "$go_mod"
		rm -f "$go_mod_orig"
	fi
	if [ -f "$go_sum_orig" ]; then
		cp "$go_sum_orig" "$go_sum"
		rm -f "$go_sum_orig"
	fi
	rm -rf "$work"
}
# One build or dev session at a time: both patch a copy and edit go.mod.
lock="$root/.mygo-patched.lock"
if ! mkdir "$lock" 2>/dev/null; then
	echo "dev-gui: another MyGo build or dev session is running (remove .mygo-patched.lock if not)" >&2
	exit 1
fi
trap 'cleanup; rmdir "$lock" 2>/dev/null || true' EXIT INT TERM

# A run killed before its cleanup left the replace in go.mod.
if [ -f "$go_mod_orig" ]; then
	cp "$go_mod_orig" "$go_mod"
	rm -f "$go_mod_orig"
fi
if [ -f "$go_sum_orig" ]; then
	cp "$go_sum_orig" "$go_sum"
	rm -f "$go_sum_orig"
fi

mygo_dir=$(cd apps/masir-gui && go mod download -json github.com/egoist/mygo |
	sed -n 's/.*"Dir": "\(.*\)".*/\1/p')
if [ -z "$mygo_dir" ] || [ ! -d "$mygo_dir" ]; then
	echo "dev-gui: cannot find the MyGo module source" >&2
	exit 1
fi

rm -rf "$work"
cp -R "$mygo_dir" "$work"
chmod -R u+w "$work"
printf 'Patching MyGo...\n'
for patch in "$root"/patches/mygo-*.patch; do
	if ! git apply -p1 --directory=.mygo-patched --whitespace=nowarn "$patch"; then
		echo "dev-gui: $(basename "$patch") does not apply to mygo $(cd apps/masir-gui && go list -m -f '{{.Version}}' github.com/egoist/mygo)" >&2
		exit 1
	fi
done
cp "$go_mod" "$go_mod_orig"
cp "$go_sum" "$go_sum_orig"
(cd apps/masir-gui && go mod edit -replace github.com/egoist/mygo=../../.mygo-patched)

# mygo dev compiles with go build, which reads the tag from GOFLAGS.
(cd apps/masir-gui && GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=mygo_patched" \
	MASIR_BIN="$root/dist/masir" go tool mygo dev)

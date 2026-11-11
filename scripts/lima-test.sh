#!/bin/bash
# Linux checks in Lima VMs: the systemd unit, its capabilities, the TUN, the sandbox and
# SELinux. It is not part of `make test` because it boots virtual machines and downloads
# cloud images on the first run.
#
#   bash scripts/lima-test.sh                 # ubuntu, arch, fedora
#   bash scripts/lima-test.sh arch fedora     # only these
#   bash scripts/lima-test.sh --geo           # also update geo data in each guest
#   bash scripts/lima-test.sh --recreate      # delete and boot the VMs again
#   bash scripts/lima-test.sh --keep          # leave the VMs running afterwards
#   bash scripts/lima-test.sh --clean         # remove the test VMs afterwards
#
# Needs limactl (brew install lima on macOS, https://lima-vm.io on Linux). On Apple
# Silicon the Arch image only boots with QEMU, so `brew install qemu` is needed there.
set -u

root=$(cd "$(dirname "$0")/.." && pwd)
conf="$root/scripts/lima/distros.conf"
guest_script="$root/scripts/lima/guest-test.sh"
port=${MASIR_LIMA_PORT:-8793}
geo=0
recreate=0
keep=0
clean=0
only=""
passed=""
failed=""
skipped=""

usage() {
	sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--geo) geo=1 ;;
	--recreate) recreate=1 ;;
	--keep) keep=1 ;;
	--clean) clean=1 ;;
	--only) shift; only=${1:-} ;;
	-h | --help) usage 0 ;;
	-*) echo "unknown flag: $1" >&2; usage 2 ;;
	*) only=${only:+$only,}$1 ;;
	esac
	shift
done

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
die() {
	printf 'lima-test: %s\n' "$*" >&2
	exit 2
}

command -v limactl >/dev/null 2>&1 ||
	die "limactl is not installed. macOS: brew install lima; Linux: https://lima-vm.io"
[ -f "$conf" ] || die "missing $conf"

arch=$(go env GOARCH) || die "go is needed to cross-compile the Linux binary"
case "$arch" in
arm64) qemu_bin=qemu-system-aarch64 ;;
amd64) qemu_bin=qemu-system-x86_64 ;;
*) die "unsupported host architecture: $arch" ;;
esac

# The subscription server: the guests fetch one share link from the host, so they need
# no python, no pacman and no network of their own to test the TUN.
sub_dir=$(mktemp -d "${TMPDIR:-/tmp}/masir-lima.XXXXXX") || die "mktemp failed"
sub_url=""
server_pid=""
cleanup() {
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null
	rm -rf "$sub_dir"
}
trap cleanup EXIT INT TERM

if command -v python3 >/dev/null 2>&1; then
	cat >"$sub_dir/sub" <<'EOF'
vless://b831381d-6324-4d53-ad4f-8cda48b30811@127.0.0.1:443?encryption=none&security=none&type=tcp#lima-test
EOF
	(cd "$sub_dir" && exec python3 -m http.server "$port" --bind 0.0.0.0 >/dev/null 2>&1) &
	server_pid=$!
	sub_url="http://host.lima.internal:$port/sub"
else
	say "note: no python3 on the host, so the TUN step will be skipped in every guest"
fi

bin="$sub_dir/masir"
step "building the Linux/$arch binary"
(cd "$root/apps/masir" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -o "$bin" .) ||
	die "go build failed"

logs="$root/dist/lima-logs"
mkdir -p "$logs" || die "cannot create $logs"

wanted() {
	[ -z "$only" ] && return 0
	case ",$only," in *",$1,"*) return 0 ;; esac
	return 1
}

# wait_ready waits until the guest answers over SSH, or gives up and says so.
wait_ready() {
	i=0
	while [ "$i" -lt 90 ]; do
		if limactl shell "$1" -- true </dev/null >/dev/null 2>&1; then return 0; fi
		i=$((i + 1))
		sleep 2
	done
	return 1
}

ensure_vm() {
	vm=$1
	template=$2
	vmtype=$3

	if [ "$recreate" = 1 ] && limactl list -q 2>/dev/null | grep -qx "$vm"; then
		limactl delete -f "$vm" </dev/null >/dev/null 2>&1
	fi

	if limactl list -q 2>/dev/null | grep -qx "$vm"; then
		limactl start --tty=false "$vm" </dev/null >/dev/null 2>&1 || return 1
	else
		set -- start --name "$vm" --tty=false --containerd none
		if [ "$vmtype" = "qemu" ]; then
			command -v "$qemu_bin" >/dev/null 2>&1 ||
				die "$qemu_bin is missing; the arch guest needs QEMU (macOS: brew install qemu)"
			set -- "$@" --vm-type qemu
		fi
		limactl "$@" "$template" </dev/null >"$logs/$vm.start.log" 2>&1 || {
			# A cloud image download can end early; one retry is usually enough.
			say "   start failed, retrying once: $(tail -1 "$logs/$vm.start.log")"
			limactl delete -f "$vm" </dev/null >/dev/null 2>&1
			limactl "$@" "$template" </dev/null >>"$logs/$vm.start.log" 2>&1 || return 1
		}
	fi
	wait_ready "$vm"
}

run_guest() {
	vm=$1
	cfg="$HOME/.lima/$vm/ssh.config"
	[ -f "$cfg" ] || {
		say "   no ssh config at $cfg"
		return 1
	}
	# scp, not limactl copy: the latter drives the guest's rsync, which some older
	# images ship broken.
	scp -q -F "$cfg" -o BatchMode=yes "$bin" "lima-$vm:/tmp/masir" </dev/null || return 1
	ssh -F "$cfg" -o BatchMode=yes "lima-$vm" \
		"sudo env SUB_URL='$sub_url' GEO='$geo' MASIR_LIMA_DISTRO='$vm' bash -s" \
		<"$guest_script"
}

summary() {
	printf '\n===== summary =====\n'
	[ -n "$passed" ] && printf '  passed:  %s\n' "$passed"
	[ -n "$skipped" ] && printf '  skipped: %s\n' "$skipped"
	[ -n "$failed" ] && printf '  failed:  %s\n' "$failed"
	printf '  logs:    %s\n' "$logs"
	[ -z "$failed" ]
}

while IFS=$'\t' read -r distro template vmtype flags <&3; do
	case "$distro" in '' | '#'*) continue ;; esac
	wanted "$distro" || continue

	step "$distro ($template)"
	if [ "$flags" = "qemu-required" ] && ! command -v "$qemu_bin" >/dev/null 2>&1; then
		say "   skipped: $qemu_bin is missing"
		skipped="$skipped $distro"
		continue
	fi

	if ! ensure_vm "masir-test-$distro" "$template" "$vmtype"; then
		say "   FAILED to start (log: $logs/masir-test-$distro.start.log)"
		failed="$failed $distro"
		continue
	fi

	log="$logs/$distro.log"
	say "   log: $log (tail -f it to watch this guest)"
	run_guest "masir-test-$distro" 2>&1 | tee "$log"
	status=${PIPESTATUS[0]}
	[ "$status" = 0 ] && passed="$passed $distro" || failed="$failed $distro"

	if [ "$keep" = 0 ] && [ "$clean" = 1 ]; then
		limactl delete -f "masir-test-$distro" </dev/null >/dev/null 2>&1
	elif [ "$keep" = 0 ]; then
		limactl stop "masir-test-$distro" </dev/null >/dev/null 2>&1
	fi
done 3<"$conf"

summary

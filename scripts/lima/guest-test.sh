#!/bin/bash
# Runs inside a Lima guest as root. Installs the Linux binary as the service and checks
# what the repo promises on Linux: the unit's capabilities, the sandbox, the TUN, the
# service's ownership of its socket, and SELinux where it exists.
#
#   SUB_URL=https://… GEO=1 bash guest-test.sh
#
# Every check prints PASS, FAIL or SKIP; the exit status is non-zero if anything failed.
set -u

[ -x /tmp/masir ] || {
	echo "FAIL: /tmp/masir is missing"
	exit 2
}

pass=0
fail=0
skip=0
ok() {
	printf '   PASS  %s\n' "$*"
	pass=$((pass + 1))
}
bad() {
	printf '   FAIL  %s\n' "$*"
	fail=$((fail + 1))
}
note() {
	printf '   SKIP  %s\n' "$*"
	skip=$((skip + 1))
}

# The guest's regular user: Lima maps the host account, so it is 501 on macOS and the
# host uid on Linux. Never pick a system account (uid < 500, home outside /home).
u=$(awk -F: '$3>=500 && $3<65534 && $6 ~ /^\/home\// {print $1; exit}' /etc/passwd)
[ -n "$u" ] || {
	echo "FAIL: no regular user with a home under /home"
	exit 2
}
uid=$(id -u "$u")

echo "   host: $(grep PRETTY_NAME /etc/os-release | cut -d= -f2- | tr -d '"') · kernel $(uname -r) · systemd $(systemctl --version | awk 'NR==1 {print $2}')"
echo "   user: $u (uid $uid)"

install -m 0755 /tmp/masir /usr/local/bin/masir
/usr/local/bin/masir daemon enable --user "$uid" >/tmp/enable.log 2>&1 || {
	bad "masir daemon enable failed: $(tail -1 /tmp/enable.log)"
	exit 1
}
systemctl reset-failed masird >/dev/null 2>&1
systemctl restart masird >/dev/null 2>&1

i=0
while [ "$i" -lt 40 ] && [ "$(systemctl is-active masird 2>/dev/null)" != active ]; do
	i=$((i + 1))
	sleep 0.5
done

[ "$(systemctl is-active masird 2>/dev/null)" = active ] &&
	ok "the service is active" ||
	bad "the service is not active: $(systemctl show -p Result --value masird)"

caps=$(systemctl show -p CapabilityBoundingSet --value masird 2>/dev/null)
case "$caps" in
*cap_chown*cap_dac_override*cap_net_admin* | *cap_net_admin*cap_chown*cap_dac_override* | *cap_dac_override*cap_chown*cap_net_admin* | *cap_chown*cap_net_admin*cap_dac_override* | *cap_net_admin*cap_dac_override*cap_chown* | *cap_dac_override*cap_net_admin*cap_chown*)
	ok "capabilities are bounded to $caps"
	;;
*)
	bad "CapabilityBoundingSet = ${caps:-empty}, want cap_chown cap_dac_override cap_net_admin"
	;;
esac

[ "$(systemctl show -p ProtectSystem --value masird 2>/dev/null)" = full ] &&
	ok "ProtectSystem=full" ||
	bad "ProtectSystem is not full"

# The updater reconciles the service definition: a unit installed by an older masir must
# be rewritten from the service's binary and applied, not left until the next reinstall.
unit=/etc/systemd/system/masird.service
if [ -f "$unit" ]; then
	cp "$unit" /tmp/unit.before
	sed -i '/^CapabilityBoundingSet=/d' "$unit"
	systemctl daemon-reload
	systemctl restart masird
	sleep 2
	if /usr/local/bin/masir daemon sync-unit --user "$uid" >/tmp/sync-unit.log 2>&1 &&
		grep -q '^CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_NET_ADMIN' "$unit" &&
		[ "$(systemctl is-active masird 2>/dev/null)" = active ]; then
		ok "daemon sync-unit reconciles an old definition and the service comes back"
	else
		bad "daemon sync-unit did not restore the unit: $(tail -1 /tmp/sync-unit.log)"
	fi
else
	note "no systemd unit file to reconcile"
fi

status=$(su - "$u" -c "/usr/local/bin/masir status --no-check" 2>&1)
case "$status" in
*"Active (privileged, service)"*) ok "masir status reaches the service as $u" ;;
*) bad "masir status as $u: $(printf '%s' "$status" | head -3 | tr '\n' ' ')" ;;
esac

# The socket belongs to the service: a second, unsupervised daemon must not take it.
if timeout 8 /usr/local/lib/masir/masir daemon run --user "$uid" >/tmp/stray.log 2>&1; then
	bad "a stray daemon exit 0 instead of refusing"
elif grep -q "the masir service owns this socket" /tmp/stray.log; then
	ok "a stray root daemon refuses to take the service's socket"
else
	bad "stray daemon did not refuse: $(tail -1 /tmp/stray.log)"
fi
[ "$(systemctl is-active masird 2>/dev/null)" = active ] &&
	ok "the service survived the stray daemon" ||
	bad "the stray daemon stopped the service"

pid=$(systemctl show -p MainPID --value masird 2>/dev/null)
if [ -n "$pid" ] && command -v nsenter >/dev/null 2>&1; then
	if nsenter -t "$pid" -m -- sh -c 'touch /usr/bin/.masir-probe' 2>/dev/null; then
		bad "/usr/bin is writable inside the unit"
	else
		ok "/usr/bin is read-only inside the unit"
	fi
	if nsenter -t "$pid" -m -- sh -c 'touch /usr/local/lib/masir/.probe && rm -f /usr/local/lib/masir/.probe' 2>/dev/null; then
		ok "/usr/local/lib/masir stays writable for updates"
	else
		bad "/usr/local/lib/masir is read-only inside the unit"
	fi
	if nsenter -t "$pid" -m -- sh -c 'touch /etc/.masir-probe' 2>/dev/null; then
		bad "/etc is writable inside the unit"
	else
		ok "/etc is read-only inside the unit"
	fi
else
	note "nsenter or the service PID is missing; sandbox probes not run"
fi

if [ -n "${SUB_URL:-}" ]; then
	su - "$u" -c "/usr/local/bin/masir sub add '$SUB_URL' --name lima" >/tmp/sub.log 2>&1 ||
		su - "$u" -c "/usr/local/bin/masir sub update --name lima" >/tmp/sub.log 2>&1
	node=$(su - "$u" -c "/usr/local/bin/masir sub list --nodes" 2>/dev/null | awk '$2=="lima" {print $1; exit}')
	if [ -z "$node" ]; then
		bad "the test subscription did not parse: $(tail -1 /tmp/sub.log)"
	else
		su - "$u" -c "/usr/local/bin/masir use '$node'" >/tmp/use.log 2>&1
		su - "$u" -c "/usr/local/bin/masir connect" >/tmp/connect.log 2>&1
		if ip -br link 2>/dev/null | grep -qE '^(tun|utun)'; then
			ok "TUN is up after masir connect"
		else
			bad "no tun device after connect: $(tail -2 /tmp/connect.log | tr '\n' ' ')"
		fi
		su - "$u" -c "/usr/local/bin/masir status --no-check" 2>/dev/null | grep -q "TUN" &&
			ok "status reports TUN mode" ||
			bad "status does not report TUN mode"
	fi
else
	note "no subscription server (python3 missing on the host); TUN not tested"
fi

if command -v getenforce >/dev/null 2>&1; then
	if [ "$(getenforce)" != Enforcing ]; then
		setenforce 1 >/dev/null 2>&1
	fi
	if [ "$(getenforce)" = Enforcing ]; then
		domain=$(ps -eZ 2>/dev/null | awk '$5=="masir" {print $1; exit}')
		case "$domain" in
		*unconfined_service_t*) ok "the daemon runs in unconfined_service_t" ;;
		*) bad "the daemon runs in ${domain:-an unknown domain}, want unconfined_service_t" ;;
		esac
		avc=$(journalctl _TRANSPORT=audit --no-pager 2>/dev/null | grep -c 'denied')
		[ "$avc" = 0 ] && ok "SELinux logged no denial" || bad "SELinux logged $avc denials"
		if command -v semanage >/dev/null 2>&1; then
			semanage fcontext -l 2>/dev/null | grep -q "/usr/local/lib/masir" &&
				ok "an fcontext rule keeps the label across a relabel" ||
				bad "no fcontext rule for /usr/local/lib/masir"
		else
			note "semanage is missing; the fcontext rule cannot be checked"
		fi
	else
		note "SELinux is present but not enforcing"
	fi
else
	note "no SELinux on this distro"
fi

if [ "${GEO:-0}" = 1 ]; then
	if su - "$u" -c "/usr/local/bin/masir update geo --force" >/tmp/geo.log 2>&1; then
		ok "update geo finished with the service running"
	else
		bad "update geo failed: $(tail -2 /tmp/geo.log | tr '\n' ' ')"
	fi
	[ "$(systemctl is-active masird 2>/dev/null)" = active ] &&
		ok "the service is still active after the update" ||
		bad "the service stopped during the update"
else
	note "geo update not requested (--geo)"
fi

printf '   %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skip"
[ "$fail" = 0 ]

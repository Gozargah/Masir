# macOS TUN DNS

On macOS, installing the TUN routes does not change the system resolver. DHCP DNS
can remain directly reachable on a physical interface and never enter Xray. The
privileged daemon therefore manages network-service DNS while Masir's TUN runs.
Windows, Linux and FreeBSD continue to use their existing Xray settings.

## Connection and recovery

After starting the TUN, Masir verifies that every configured TUN DNS address routes
to that interface, using a local routing-table lookup. It does not send a DNS probe,
run `dig`, or require a successful external lookup before applying DNS settings.
The addresses come from `tunDns`, or `dnsServers` when `tunDns` is empty. An address
that still routes over Wi-Fi/Ethernet fails startup instead of silently bypassing
interception. Upstream DNS failures do not prevent installing the override.

Masir then uses SystemConfiguration to replace `ServerAddresses` for enabled
physical network services in the current network location, including Wi-Fi,
Ethernet and tethering. VPN services and domain-specific resolvers are left alone.
Other DNS properties, including search domains, are preserved.

Original **configured** addresses are recorded by service UUID in
`/var/db/masir-dns/recovery.json`, before preferences are committed. No configured
addresses means automatic DNS; recovery removes the override rather than saving
an old DHCP server as a manual address. The directory is root-owned and a
machine-wide lock prevents concurrent daemons from owning DNS.

SystemConfiguration notifications handle new services, network-location changes,
DHCP/network changes and wake. Services leaving the current location have their
original DNS restored. The manager restores before replacing/removing the TUN,
on power-off (including Always-on Proxy), shutdown, and update/restart. An idle or
proxy-only privileged daemon also recovers a journal left by a previous crash.
If restoration fails, the journal remains for the next recovery attempt.

Restoration only changes a service whose configured addresses still match the
Masir override. If the user, administrator or another VPN replaces those addresses
while connected, Masir logs a warning and leaves that service alone until the TUN
reconnects; that service may then resolve outside Masir. This avoids fighting
external configuration and overwriting a person's new settings at disconnect.

## Scope and limitations

This manages ordinary unicast system DNS. It is not a DNS kill switch. During a
crash, before recovery, or after TUN routes disappear, a public DNS address can
become directly reachable. A strict fail-closed mode needs separate egress policy.
Startup route checks do not eliminate handover races or verify upstream availability.

IPv4 and IPv6 nameservers are overridden/restored together. An IPv4 DNS transport
can resolve AAAA records. IPv6 TUN resolver addresses must have a working IPv6 TUN
route; merely listing an IPv6 gateway in Xray's macOS configuration does not
configure that address on the interface.

Scoped/supplemental DNS (including corporate VPN and `/etc/resolver` rules),
Bonjour/multicast DNS, application-owned encrypted DNS and Private Relay require
separate policy. Captive portals and local-only names may require disconnecting or
using an appropriate split-DNS configuration.

This feature does not change Xray's shared DNS outbound. A/AAAA hijacking and the
existing handling of other query types remain unchanged; in particular a direct
fallback for HTTPS/SVCB, TXT, MX or SRV is not protected DoH forwarding.

## Verification

Use `scutil --dns` to inspect the effective resolver configuration. Capture UDP and
TCP port 53 on the current physical interface and Masir's actual utun interface.
Compare `dig @<configured-address> example.com`, `dig +tcp ...`, and a fresh lookup
through `dns-sd -G v4v6 <hostname>` or an application using the system resolver.
Cached answers produce no packets. Verify Xray's logs show `dns-out` and the intended
DoH proxy route. Plaintext bootstrap DNS and non-A/AAAA forwarding remain subject
to the existing outbound rules.

Exercise automatic/manual DNS, IPv6 DNS, Wi-Fi/Ethernet handover, network locations,
sleep/wake, power-off, daemon restart and a killed-daemon recovery. On disconnect,
check configured DNS preferences as well as `scutil --dns`: automatic DNS should
follow the current network's DHCP data again.

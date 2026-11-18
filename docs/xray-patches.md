# Patched Xray builds

`patches/xray-xicmp-outbound-interface.patch` applies to the Xray-core version
pinned in `libs/masir-daemon/go.mod` (`676eaf66f8f0`). Like the MyGo patches, it is
applied to a downloaded source copy; the upstream module is not vendored.

The patch applies outbound socket controllers to XICMP's IPv4/IPv6 datagram and
raw ICMP sockets using the actual configured peers. Each peer gets its own socket on first use, so peers with different egress
interfaces retain independent bindings without allocating sockets for unused
peers. Failed setup closes the rejected socket, and connection shutdown reaps
all cached sockets. On macOS, binding selects the longest matching non-TUN route for the peer
address, including connected LAN routes without a default gateway and exempts loopback before any route lookup. Host-address routes through
loopback also bypass interface binding. The patch exposes
the TUN interface binding as `InterfaceUpdater.BindSocket`. It includes regression tests
for controller invocation and failed socket cleanup.

`scripts/with-xray.sh` prepares an ignored `.xray-patched.*` directory and runs a
command with a private Go workspace replacing only Xray-core. It preserves the
caller's working directory, uses the repository's Go toolchain, and removes the
copy/workspace on exit. Tracked module/workspace files and the module cache are
not edited. Nested invocations reuse the prepared source.

Use the normal Make targets for patched CLI/desktop builds and project checks:

```sh
make build
make gui-local
make test
make test-race
```

The desktop build/dev scripts, Lima tests, and CLI release/snapshot workflows
also enter the preparer. To run a focused command with the patch:

```sh
sh scripts/with-xray.sh go test ./libs/masir-daemon/internal/xray
sh scripts/with-xray.sh go build -o dist/masir ./apps/masir
```

On macOS the probe helper detects the added `BindSocket` API through a small
interface. A prepared-build regression test requires that capability to exist.
Stock Xray builds retain their no-op helper, while prepared builds install the
binding regardless of caller-supplied build tags. The preparer does not inspect
or modify `GOFLAGS` or `-tags`; Go handles their quoting and validation directly.

`make test`/`make test-race` and CI also run the patched dependency's XICMP and
TUN packages. ICMP integration tests skip only when socket support is denied or
unavailable; CI enables Linux ping sockets and sets `XRAY_REQUIRE_ICMP=1` so
these assertions must run on capable macOS/Linux jobs. The full upstream suite
additionally needs GeoIP/GeoSite fixtures in the prepared module's `resources`
directory; it has known public DNS/ECH
failures and races that reproduce on unmodified upstream. The targeted patched
packages and Masir checks pass.

When upgrading Xray, the preparer fails if the patch no longer applies. Rebase
the patch, run the project and patched-dependency tests, and verify an XICMP
probe with TUN enabled. Remove the patch and conditional API use when upstream
provides the equivalent socket controller behavior and binding API.

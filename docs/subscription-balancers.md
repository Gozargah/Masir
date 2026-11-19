# Balanced subscription profiles

A balanced subscription entry represents one selectable profile. Masir keeps
its outbounds, routing rules, balancers, dial chains, and observatory settings.
Existing single-outbound subscription entries and active selections still
load through the single-outbound path.

## Routing

Masir first evaluates its own routing rules. When a rule selects a balanced
profile, that profile's native Xray router evaluates the connection against
the provider's rules in order. If none match, it uses the profile's first
outbound. Provider tags are scoped to the profile so multiple active and
pinned profiles can use the same names.

An internal loopback sends the routing decision back to the same profile
with the loopback's ingress tag. Missing ingress targets are rejected during
composition. Repeated steps or excessive hops are blocked before dispatch.
Loopbacks that require fresh payload sniffing and rules that distinguish
provider SOCKS and HTTP aliases remain unsupported.

## Switching and health checks

Masir installs the new profile's outbounds and observatory in the running
Xray instance, then publishes the new routing table. It can route immediately
through the provider's explicit fallback, or the first matching outbound in
provider order when no fallback is specified. Later connections use the
current observatory results. Unchanged observers keep their probe history.

The user controls the DNS resolver settings. Endpoint hostname changes
update bootstrap data live. Retired balanced profiles keep their handlers,
dial chains, and bootstrap hostnames while existing flows drain, subject to
a two-minute deadline and a cap of 256 retired handlers. Profile counters
track traffic at the dispatch boundary and exclude forced probes and chained
dials.

## Verification

The integration tests use synthetic profiles and loopback HTTP, TCP, and UDP
servers. They cover selection in both directions without a restart,
health-based fallback, provider rules and internal loopbacks, connection
draining, and the isolated latency worker.

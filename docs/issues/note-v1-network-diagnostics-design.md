**note**

# V1 — network domain: extra diagnostics

Phase-2 unit V1 adds six operations to the existing `network` domain (no new
domain, no server changes — a new manifest `category` value is what makes a
domain, and `network` already exists). Five are read-only builtins; one is a
benign auto-commit mutation.

| Op | Kind | Binary | Notes |
|---|---|---|---|
| `trace_route` | RO builtin | `traceroute` | hop-by-hop path + per-hop latency; `max_hops` clamped 1–30 (default 15); context deadline scales with the hop count |
| `whois_lookup` | RO builtin | `whois` | domain/IP registration record; output run through the 32 KB compaction (records are long) |
| `route_table` | RO builtin | `netstat` | `-rn -f inet` + `-f inet6`, labelled; fixed argv |
| `interface_stats` | RO builtin | `netstat` | `-ib`; fixed argv |
| `dns_cache_lookup` | RO builtin | `dscacheutil` | resolves through directory services (the path apps use); complements `dns_lookup` (dig) |
| `flush_dns_cache` | mutation, auto-commit / low / irreversible | `dscacheutil` | `-flushcache`; nil inverse |

## Why `flush_dns_cache` is auto-commit + irreversible

Flushing the resolver cache is self-healing — the cache repopulates on demand —
so it sits in the same low-stakes lane as `display_sleep`: run immediately, no
execute-token round trip (registry-enforced none/low risk). It is *irreversible*
only in the sense that a flushed cache cannot be "un-flushed" (the prior entries
are gone), so the plan carries a nil `Inverse` and the auto-commit path appends
its own "This cannot be undone." note. The preview also records the scope limit:
`dscacheutil -flushcache` clears dscacheutil's cache but does **not** restart
mDNSResponder; a full DNS reset (`killall -HUP mDNSResponder`) needs admin rights
and is deliberately out of scope.

## Injection posture

None of the underlying binaries (`traceroute`, `whois`, `dscacheutil`) exposes a
usable `--` end-of-options terminator, and each would read a dash-leading value
as one of its own flags (`whois -h` is an especially sharp example — it redirects
the query to an arbitrary WHOIS server). So all three host-taking ops funnel
their model input through the **same** `validateNetworkHost` allowlist that
already guards `ping_host`/`dns_lookup`, applied before any argv is assembled.
`route_table`/`interface_stats` take no input, and `flush_dns_cache` is a fixed
constant, so those three carry no injection surface. The three new host-taking
ops are recorded in `reviewedFreeTextBuiltins`; `TestNetworkDiagnostics_RejectHostileHost`
is the per-op `-e`-lands-as-data regression required by CLAUDE.md §4.

## Dropped from scope (Phase-2 owner decisions)

- **`powermetrics`** — requires root; no non-privileged mode. Partial coverage
  comes from `top` (V4) + the existing `pmset` reads.
- **`nslookup`** — redundant with the existing `dns_lookup` (dig).

## Eval / verification note

Unit tests (routing-independent: argv pinning, the hostile-host battery, clamp
bounds, the flush mutator) are green in-package. The four new eval cases in
`evals/cases/network_diagnostics.json` (trace_route / whois / route_table
selection + the flush auto-commit) load cleanly and were confirmed via the
runevals dry-run. Fresh-server routing and live execution of the new ops could
**not** be exercised in-session: the MCP server attached to the authoring session
is the pre-build binary and still lists only the original seven network ops, so
the new operations are only reachable from a rebuilt server (the `go run
./cmd/runevals` / CI path). That fresh-server routing + live pass is the pending
manual confirmation for this unit.

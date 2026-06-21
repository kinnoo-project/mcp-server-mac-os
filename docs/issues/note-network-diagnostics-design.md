**note**

The `network` capability (manifest `internal/registry/manifests/network.json`,
builtins `internal/engine/builtins_network.go`) adds a new top-level domain tool
with seven **read-only** operations: `current_network` (interface, IPv4, MAC,
subnet mask + usable-host capacity, default gateway), `dns_servers`, `ping_host`,
`dns_lookup`, `listening_ports`, `lan_devices` (passive ARP-cache read), and
`scan_lan` (active, bounded ping sweep). All are in-process builtins that resolve
their binaries through the policy layer and shape parsed output into text — no
new MCP wiring was needed because `Server.Domains()` derives the tool set from the
registry's categories.

Design choices worth recording:

- **Host validation is the security crux, not a `--` terminator.** `ping_host`
  and `dns_lookup` take a model-controlled host. `dig` has no end-of-options
  terminator and parses leading-`-` (flags), `@` (alternate server), and `+`
  (query options) specially, so the defense is a strict allowlist
  (`validateNetworkHost`: charset `[A-Za-z0-9.:-]`, no leading `-`, length ≤ 253)
  applied before argv assembly. `ping` additionally gets a belt-and-suspenders
  `--`. This is covered by the mandatory accept/reject regression table per
  `.claude/rules/darwin-execution.md` §4.

- **`scan_lan` is bounded by construction.** It only ever pings addresses it
  computes from the local subnet (never model input), refuses subnets wider than
  /24, caps concurrency, and runs under a 20s timeout layered beneath the request
  context so a dropped request stops the sweep.

- **LAN discovery ships in two flavours deliberately.** `lan_devices` is instant
  and traffic-free but only shows already-contacted hosts; `scan_lan` is the
  opt-in fuller sweep. The split keeps the cheap/passive path the default.

- **Diagnostics are model-composed, not a fixed bundle.** There is no
  `diagnose_connectivity` operation; prompts like "I can't reach the internet"
  are answered by the model chaining `current_network` → `ping_host` (gateway) →
  `ping_host` (a public IP) → `dns_lookup`. The operation summaries are written to
  make that composition obvious.

- **Bluetooth was enriched, not toggled.** `bluetooth_status` (in the `system`
  domain) now also lists paired-but-not-connected devices. Turning Bluetooth on/off
  has no first-party CLI (`blueutil` is third-party and outside the trusted
  `/bin`,`/sbin`,`/usr/bin`,`/usr/sbin` dirs), so toggling remains an
  `open_settings(pane='bluetooth')` hand-off, and the status output points the
  user there.

**issue**

`set_dns` (and any other mutating network configuration) was deliberately deferred
this round. `networksetup -setdnsservers` is reversible in principle (capture the
prior resolver list at stage time, re-apply on undo, which fits the
stage → commit → undo model cleanly), but it generally requires administrator
rights, so it needs a privilege-failure-detection-and-reporting story before it
can be shipped without hanging or failing cryptically. Tracked for a future round
(no `**fixed**` marker until it actually ships — read-only diagnostics only for now).

# Capability Expansion — Phase 2 ("V units", V0–V10)

> The roadmap for the second wave of capability build-out, driven by the
> [Darwin Binary Coverage Audit](darwin-binary-coverage-audit.html). That audit
> found the server invokes only **49 of the 1,249 executables** in its four
> trusted directories (`/usr/bin`, `/usr/sbin`, `/bin`, `/sbin`) — this phase wires
> up eleven of the most useful unused binary groups. Everything here fits the
> existing grain described in the [capability engine](macos-mcp-capability-engine.md):
> a JSON manifest entry plus either the generic argv builder, a named builder over
> an allowlisted binary, or an in-process builtin — reads through `query`, mutations
> through `plan_action` → `commit_action` → `undo_action`.

## Context

Jerry picked 11 binary groups from the audit to bring online: keychain,
backup/disk management, media/document conversion, `caffeinate`, Shortcuts,
diagnostics upgrades (`log`/`top`/`codesign`/`spctl`/`csrutil`), extra network
diagnostics, `dscacheutil`, `hidutil`, sharing-service status, and SSH. Each group
maps to concrete end-user intents, lands in a domain (19 exist today; 3 new ones are
justified), and ships as one PR-sized unit into `build` via `/ship-pr` — Copilot
review, then stop at ready-to-merge for a manual merge.

## Owner decisions (2026-07-04)

1. **Keychain = metadata only.** Never return secret values — the `-w`/`-g` flags
   are structurally forbidden, so the server can surface *that* a credential exists
   (service, account, label, modification date) but never the password itself.
2. **SSH = open Terminal.app** with the fully-constructed `ssh` command (staged;
   requires Automation TCC for Terminal). Key discovery returns fingerprints only,
   never private-key material.
3. **Deny-list: all five come off** (`tmutil`, `diskutil`, `hdiutil`, `csrutil`,
   `spctl`) with narrow verb pinning enforced by an invariant test. Mounts/attaches
   are OK to execute. **Destructive operations (eject/unmount) are never executed** —
   instead the server returns the exact command for the user to run manually, with a
   clear consequences warning. This is the new **"advisory command" pattern** for this
   phase.
4. **hidutil remap = curated enum** of common, vetted remaps only (e.g. Caps Lock →
   Escape) — never free-form key-mapping JSON from the model.

## Dropped, with rationale

- **`powermetrics`** — requires root; no non-privileged mode. Partially covered by
  `top` plus the existing `pmset` reads.
- **`nslookup`** — redundant; the existing `dns_lookup` (via `dig`) already covers it.
- **Keychain writes** — cut per decision 1, matching the "exclude low-value
  destructive ops" precedent.

## Domain mapping

| Binary group | Capability (user intent) | Domain |
|---|---|---|
| traceroute, whois, netstat, dscacheutil | "why is my network slow / who owns this domain / flush DNS" | **network** (existing) |
| sips, textutil, qlmanage | "convert/resize this image, docx→pdf, preview this file" | **filesystem** (existing) |
| caffeinate | "keep my Mac awake for an hour" | **system** (existing) |
| log, top | "what's spamming the log / live CPU snapshot" | system + process (existing) |
| codesign, spctl, csrutil, xattr (quarantine) | "is this app safe / is SIP on" | **security** (NEW) |
| security (keychain) | "do I have a saved password for X" | **security** (NEW) |
| tmutil, diskutil, hdiutil | "when did Time Machine last run / mount this dmg" | **storage** (NEW) |
| shortcuts | "run my Morning Routine shortcut" | **shortcuts** (NEW) |
| hidutil, sharing, systemsetup | "map caps lock to escape / is screen sharing on" | **system** (existing) |
| ssh, ssh-keygen | "SSH into my server as ubuntu" | **network** (existing) |

**19 → 22 domains** (+security, +storage, +shortcuts). **25 MCP tools total** by V10
(22 domain tools + `execute`/`undo`/`pipeline`).

## Units (each = one PR into `build`, shipped in order)

- **V0 — docs.** This note + the binary-audit HTML artifact. No code.
- **V1 — network: extra diagnostics.** `trace_route`, `whois_lookup`, `route_table`,
  `interface_stats`, `dns_cache_lookup`, `flush_dns_cache`.
- **V2 — filesystem: media & document conversion.** `image_info`, `convert_image`,
  `resize_image`, `convert_document`, `quicklook_thumbnail` (trash-inverse for created
  files; new destination paths never overwrite).
- **V3 — system: keep-awake (`caffeinate`).** `keep_awake` / `allow_sleep` /
  `sleep_assertions`; adds a detached-process path to the engine.
- **V4 — diagnostics reads.** `system_log` (Go-composed, injection-guarded predicate),
  `top_processes`, `thermal_state`.
- **V5 — NEW domain `security`, part 1.** `verify_signature`, `gatekeeper_check`,
  `sip_status`, `quarantine_info`. First deny-list edit (`csrutil`, `spctl` removed);
  introduces the reusable verb-pinning invariant test.
- **V6 — security, part 2: keychain metadata.** `find_credential`,
  `find_internet_credential`, `list_keychains` — attribute allowlist, never secrets.
- **V7 — NEW domain `storage`.** Time Machine status, volume/backup listing, volume
  info, mount/attach, detach, and the advisory `eject_volume`. Second deny-list edit
  (`tmutil`, `diskutil`, `hdiutil`).
- **V8 — NEW domain `shortcuts`.** `list_shortcuts`, `run_shortcut` (staged, pinned
  into `dangerousOps`). Un-tables `shortcuts-runner-deferred.md`.
- **V9 — system: input remapping + sharing status.** `remap_key` (curated enum,
  inverse restores prior mapping), `key_remap_status`, `sharing_status`, plus a
  `sharing` Settings pane.
- **V10 — network: SSH connect.** `list_ssh_keys` (fingerprints only),
  `list_ssh_hosts`, `ssh_connect` (staged; opens Terminal with a fully-validated
  command). Full eval suite runs to close the phase.

## Verification themes (every unit)

Same 8-point checklist as the prior U0–U17 phase: manifest entry → builder tier →
StagedPlan discipline → injection guards (`--`/dash-guards + `-e`-lands-as-data
regression) → TCC mapping → unit tests → eval cases (CI-safe "A" + manual "M") →
docs. The two security gates — `injection_sweep_test.go` and
`security_invariants_test.go` — must stay green; V5 and V7 consciously *extend* the
verb-pinning invariant, never weaken it. Tool-surface count bumps 22 → 23 (V5) → 24
(V7) → 25 (V8).

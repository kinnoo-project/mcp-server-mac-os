**note**

V4 — diagnostics reads: `system_log`, `top_processes`, `thermal_state`.

Three read-only operations added across the existing `system` and `process`
domains (no new domain, no new MCP tool — they slot into tools the model already
sees). Part of the Phase-2 "V units" roadmap
(`docs/ideas/capability-expansion-phase2.md`, V4).

## What each does

- **`system_log`** (system domain) — a recent slice of the unified system log via
  `log show --style syslog --last <N>m`, where `N` is a clamped integer (1–30).
  Two optional filters, `process` and `subsystem`, narrow the results. Answers
  "what's spamming the log?" and "what did app X just report?". Output is bounded
  by the shared 32 KB compaction, and the header tells the model to narrow with
  the filters when the window is truncated.

- **`top_processes`** (process domain) — a live, instantaneous %CPU/%MEM snapshot
  from `top`. It complements the existing ps-based `list_processes`, whose %CPU
  is an average that lags; `top_processes` is the "right this second" view.

- **`thermal_state`** (system domain) — whether the CPU is being throttled to
  manage heat, from `pmset -g therm`, rendered as a plain-language verdict.

## Design decisions

**`system_log` predicate is composed, never free-form (security-critical).** The
model never supplies a raw `--predicate` string. The two filter values are
validated against strict charsets — `process` rejects quotes, backslashes,
control characters and a leading dash; `subsystem` must match a reverse-DNS
allowlist (`[A-Za-z0-9][A-Za-z0-9.-]*`) — and are then composed **in Go** into a
`process == "X" && subsystem == "Y"` expression. Because the surrounding quotes
are added by us and the value cannot contain a quote or backslash, a hostile
value can neither close its own string and inject predicate syntax nor be parsed
by `log` as one of its own flags. `composeLogPredicate` is a pure function with an
accept/reject regression table (`TestComposeLogPredicate`); `system_log` is listed
in `reviewedFreeTextBuiltins` as required by the injection-sweep gate. `log` is
resolved from the trusted system directories by the policy layer (no policy
change was needed — `/usr/bin/log` is present).

**`top_processes` reads the SECOND sample and puts command last.** `top -l 2`
prints two samples; the first reports CPU accumulated since boot (a meaningless
average), so only the second (real-interval) sample is parsed. The `-stats` list
is `pid,cpu,mem,command` — command last — so, exactly as with the ps-based
builtins, the fixed-width leading columns are single tokens and the space-bearing
command name is simply the trailing remainder. No brittle column-offset parsing.

**No new domain / tool count unchanged.** These operations belong to intents the
`system` and `process` tools already cover, so they were added to those manifests
rather than as a new domain. `TestIntegration_ToolSurface` stays at its current
count; the count bumps land at V5 (`security`), V7 (`storage`), V8 (`shortcuts`).

## Dropped with rationale

**`powermetrics` is deliberately not wired up.** It is the tool that would give
per-process GPU/energy accounting and finer thermal detail, but it refuses to run
without root and offers no non-privileged mode. This server never escalates
privileges, so `powermetrics` cannot be offered honestly. The non-privileged
subset is covered instead by `top` (live per-process CPU/mem) and `pmset -g therm`
(thermal throttling), plus the existing `gpu_stats` (whole-device GPU) and
`cpu_load`/`memory_stats`. This matches the "exclude low-value / unusable ops"
precedent (e.g. no force-kill in the process domain).

## Manual on-device verification

The pure helpers are covered by unit tests; the end-to-end path
(`TestDiagnosticsBuiltins_Live`, gated by `MCP_DIAGNOSTICS_LIVE=1`) was run once
on-device during development and passed against the real `log`, `top`, and
`pmset`. A live-server in-session eval run could not exercise these ops because
the MCP server binary in that session predated the change; routing was verified
from the prompts and execution from the Go live test.

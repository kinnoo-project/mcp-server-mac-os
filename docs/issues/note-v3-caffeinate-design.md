**note**

V3 — `system` domain gains a keep-awake trio built on `caffeinate` + `pmset`:
`keep_awake` (block sleep for a set time), `allow_sleep` (end any keep-awake
session), and `sleep_assertions` (read what is keeping the Mac awake). All three
extend the existing `system` tool; no new MCP tool. Design decisions worth
recording:

## The engine grew a `Command.Detach` flag — the one long-lived-helper path

`caffeinate` must keep running for the whole requested duration, long after the
tool call that started it returns. Every other command the engine runs is
short-lived and bound to the request context (so client cancellation or a
timeout kills the child). A keep-awake helper is the opposite: binding it to the
request context would kill it the instant the commit call returned.

So `Command` gained a `Detach bool`. When set, `RunCommand` routes to a new
`execDetached` (executor.go) that departs from the normal path in two ways:

- It uses `exec.Command`, **not** `exec.CommandContext` — the child is *not*
  bound to the request context, so it survives the call. Its lifetime is bounded
  instead by its own arguments (`caffeinate -t <seconds>`) plus the paired
  canceller (`allow_sleep`).
- It puts the child in its own process group (`Setpgid`) and releases the OS
  handle instead of waiting on it, detaching it from the server's signal group so
  a Ctrl-C to the server does not also stop the background session.

This is the only capability that detaches. The flag is deliberately narrow, and
`execDetached` returns immediately with the child's PID (surfaced in the commit
result) rather than any streamed output.

## Why `keep_awake` is `irreversible` (no undo token), not `reversible`

The obvious design would give `keep_awake` an inverse that kills the caffeinate
process — making it reversible with a clean undo token. That cannot work under
the engine's staging contract: a staged inverse must be **fully resolved at stage
time** (mutate.go), but the caffeinate PID is not knowable until the *detached*
process actually starts at commit time. Those two facts are irreconcilable, so a
per-plan inverse would have to be minted after the forward runs — a special case
the whole "what executes is exactly what was staged" property exists to avoid.

Rather than bolt an exception onto the staging machinery, `keep_awake` carries a
nil inverse and is classified `irreversible`. Ending a session is a first-class
separate operation (`allow_sleep`), and every session self-expires via its `-t`
timer anyway. `risk` stays `medium` (it changes power behaviour for up to hours),
so it is STAGED behind the confirmation gate; medium risk with no auto_commit and
no inverse is a valid combination (it just means "confirm, then run, no undo").
The 4-hour ceiling (14400 s) caps a forgotten session so it cannot hold the Mac
awake — and drain the battery — indefinitely.

## `allow_sleep` can only ever SIGTERM `caffeinate`, never an arbitrary process

`allow_sleep` is an auto_commit, low-risk mutation (letting a Mac sleep again is
benign and trivially reissued with `keep_awake`, mirroring `display_sleep`). It
finds the caffeinate processes to stop by reading the system's own process table
(`ps`, via the shared `builtins_process.go` helpers) and filtering to processes
named exactly `caffeinate`, owned by the current user, with PID > 1. That filter
(`caffeinatePIDs`) is the entire safety story — it is a pure function pinned by a
hostile-snapshot test so the operation can never be aimed at an unrelated
process — and the forward command is always `kill -TERM <pids>` (never a
force-kill; `killCaffeinateCommand` is pinned too). When no session is active it
returns a friendly error ("this Mac is already free to sleep") rather than an
empty `kill`.

There is no model-controlled string input anywhere in this trio: `keep_awake`
takes only a validated integer duration (rendered as a decimal argv token, so it
can never be read as a flag), and `allow_sleep`/`sleep_assertions` take no input
at all. So none of the three is a free-text mutator/builtin — nothing to add to
the `reviewedFreeText*` injection allowlists.

## `powermetrics` was not needed and stays dropped

The roadmap dropped `powermetrics` (root-only, no non-privileged mode). It is not
relevant to this unit: `pmset -g assertions` exposes exactly what keeps the Mac
awake — the assertion types and the owning processes — without any elevated
privilege, which is all `sleep_assertions` needs.

## In-session eval caveat (environment, not code)

The two CI-safe eval cases (`system_power.json`) are well-formed and load, and
the new Stage / detach / parser paths are fully covered by unit tests. The
in-session `/runevals` path, however, calls the **live connected MCP server** —
which in a given session may be a previously-built binary that predates these
ops, so it reports `unknown operation "keep_awake"`. That is a stale-server
artifact, not a routing failure: the authoritative selection check is
`go run ./cmd/runevals` (and CI), which builds the server from source. Re-run
in-session evals for new ops only after the MCP server binary is rebuilt and the
session reconnects.

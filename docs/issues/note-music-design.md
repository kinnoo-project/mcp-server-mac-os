**note**

Unit 16 of the capability-expansion roadmap: a new `application-music` tool with
one read-only operation — `now_playing` — and three transport controls —
`play_pause`, `next_track`, `previous_track`. This is the first capability to
drive Music.app, so it introduces a new MCP tool (a new `category`) and a new
Automation permission surface. This note records the design choices.

## Why a new tool, and why AppleScript

A new capability `category` in the registry is projected as a new domain tool by
the server (`internal/server/tools.go`), so `application-music` appears alongside
`application-safari`, `application-mail`, etc. — no server code change, just the
manifest. Music exposes its player state and current track only through its
scripting dictionary; there is no CLI that reads or drives them. So — like the
Mail/Notes/Safari work — each operation is a FIXED AppleScript program plus (for
the read) result parsing, implemented through the hardened `osascript` seam:
`now_playing` as an in-process builtin (`builtins_music.go`) and the three
controls as auto-commit mutators (`mutate_music.go`).

## Operations

- `now_playing` — player state (playing/paused/stopped/…) plus the current
  track's title, artist, and album, or a plain "Music is not running" line.
  Read-only / low.
- `play_pause` — toggles playback. Auto-commit / irreversible.
- `next_track` / `previous_track` — step the playback queue. Auto-commit /
  irreversible.

## Auto-commit + irreversible, and why nil Inverse

Each control is a single, immediate transport action a user expects to take
effect the instant they ask ("skip this", "pause"). Forcing a stage→execute
confirmation on "next track" would be pure friction, so these run in the
AUTO-COMMIT lane. They are modeled IRREVERSIBLE with a nil Inverse: pausing or
skipping is its own manual compensation (press play again, or `previous_track`),
and offering an "undo" that merely re-toggles state would be misleading. The
auto-commit path renders the honest "cannot be undone" suffix from the nil
Inverse — so, as with `notify`/`speak`, the previews here state the reason but do
NOT repeat that phrase.

## Never launch Music; fail cleanly when it is closed

A bare `tell application "Music"` LAUNCHES Music if it is closed — a surprising
side effect for a read, and wrong for a "skip" the user meant only for music
already playing. Every script therefore gates on `application "Music" is
running`, a LaunchServices query that does NOT start the app and needs no
Automation grant. `now_playing` reports the not-running state plainly (a benign
condition, like Safari's "no windows open"); the controls fail with a clear
error.

The controls probe readiness at STAGE time via `ensureMusicReady`, which sends a
real Apple event (`get player state`) to Music. That is deliberate: it makes a
denied-Automation failure surface at stage time — where the two-phase machinery
reports it as a clean error — instead of at commit time, where a non-zero exit
from a fire-and-forget control would be misreported as "Done". The forward script
re-checks `is running` as a belt-and-braces guard against the rare case where
Music quits between stage and commit (it errors rather than relaunching).

## Injection posture — no free-text surface

No operation takes a free-text parameter (all four are zero-parameter). There is
therefore no model-supplied value to harden, no dash-leading value to neutralize,
and — unlike the Mail/Notes reads — no `reviewedFreeTextBuiltins` entry is
required (the injection-sweep gate only demands one for free-text builtins). The
shared `osascript` seam still inserts the `--` end-of-options terminator
unconditionally, so the "data, never code" property holds structurally;
`TestMusicReadScript_UsesOptionTerminator` and the control-plan argv assertions
document it.

## TCC (Automation) permission

Music is a new Automation target — no other capability drives it. A denied
Automation permission surfaces via `musicScriptError` as a plain "grant this app
access to Music in System Settings → Privacy & Security → Automation" hint,
mirroring `safariScriptError`/`mailScriptError`.

## Optional v2 (deferred)

`search_music` / `play_track` (find and play a specific song by name) were left
out of this unit: they introduce a free-text query surface that would need an
allowlist + injection regression tests, and the roadmap scoped U16 to
now-playing + transport. The design slot is noted here for a future unit.

## Tests & evals

Pure Go halves only — no test launches `osascript`, reads live playback, or
toggles the transport. `builtins_music_test.go` covers `formatNowPlaying` (every
state, missing-field omission, untitled placeholder, not-running sentinel,
malformed line) and `interpretMusicReady` (ready / not-running / denied mapping
from synthetic results). `mutate_music_test.go` pins `musicControlPlan` (osascript
forward after `--`, nil Inverse, no duplicated "cannot be undone") and asserts
each fixed script carries its transport verb + the never-launch `is running`
guard. Evals: CI-safe routing case `now_playing_routing` (selection-only —
executing reads live playback, so real-content checks live in the manual list);
the controls are auto-commit side effects, kept OUT of the selection suite and
exercised only manually. Manual: `m_music_now_playing`,
`m_music_play_pause`, `m_music_next_track`, `m_music_previous_track`.

## Live verification status

The installed MCP server binary predates this unit, so an in-session runevals
pass cannot route the new `application-music` ops yet; wiring was verified via the
unit tests and the server's `ValidateBuilders` + tool-surface integration test
(which now asserts exactly 22 tools including `application-music`). Rebuilt `bin/`
for the next session. The live read + control paths (real Music + Automation
grant) are on the manual smoke checklist.

**note**

Unit 11 adds window management to the existing `application` domain (no new MCP
tool): a read (`list_windows`) plus three mutations (`move_window`,
`resize_window`, `minimize_window`). All four drive **System Events** — macOS's
GUI-scripting bridge — through `osascript`, reaching into other apps' windows via
the Accessibility API.

## Two permissions, not one

These are the first capabilities to need **two** separate TCC grants at once:

- **Automation** (control System Events) — the same grant `set_appearance`
  (Unit 8) introduced; a missing grant surfaces as the "-1743 / not authorized"
  error already mapped by `systemEventsScriptError`.
- **Accessibility** ("assistive access") — a distinct TCC surface required to
  *read and manipulate other apps' UI elements* (window position/size/minimized
  state). A missing grant surfaces as "-25211 / osascript is not allowed
  assistive access".

`windowScriptError` (`mutate_windowing.go`) distinguishes the two: an
assistive-access denial points the user at System Settings → Privacy & Security →
**Accessibility**, and every other denial falls back to `systemEventsScriptError`'s
**Automation** hint. Both are surfaced at **stage time**, because each mutator
probes the window's prior state (to build its inverse) before anything commits —
and since these are auto_commit, "before commit" is the only moment that exists.

## Addressing model

A window is identified by its owning **process name** + a 1-based front-to-back
**index** (1 = frontmost, the default). `list_windows` enumerates the same
(process, window) pairs the mutators act on, so the model can read first, then
target. Note System Events addresses *processes*, whose name usually — but not
always — matches the app's display name; this is a documented v1 simplification.

## Lane & reversibility

All three mutations are **reversible / low / auto_commit**: moving, resizing, or
minimizing a window is benign and everyday, so it runs immediately and offers an
undo built from the prior state observed at stage time (prior position, prior
size, or — for minimize — un-minimize, unless the window was *already* minimized,
in which case no undo is offered, mirroring `open_application` on an
already-running app). `list_windows` is **read_only / low**.

## Multi-monitor (v1: permissive)

`move_window` accepts negative x/y (a display arranged left of or above the main
one) and does not range-check coordinates; AppleScript itself clamps a window
dragged off-screen. `resize_window` requires positive dimensions bounded by a
sane ceiling (`maxWindowDimension`) purely to reject overflow-prone/absurd input
— it is not a display measurement. A future unit could resolve per-display bounds
and validate against them.

## Injection

Every model-supplied value reaches osascript as data after the `--` terminator
(the shared `osascriptCommand`/`runOsascript` seam), and the app name additionally
passes `validateAppNameValue` (non-empty, no leading dash, no control chars).
Indices and coordinates are validated ints rendered to argv strings and coerced
back `as integer` inside the fixed scripts, so there is no free-form passthrough.

## Testing / known limitation

The pure formatting, parsing, plan-building, and error-mapping halves are unit
tested without a live window (see `docs/TESTS.md`). The live move/resize/minimize
+ undo paths are **manual** eval cases (Accessibility grant + a real window). As
with prior units, the MCP binary installed in the dev session was **stale** (it
predates these ops), so in-session `/runevals` could not exercise `list_windows`
routing against the live server; wiring was verified via the engine/registry unit
tests and the server's startup `ValidateBuilders` pass, and a fresh binary was
rebuilt into `bin/` for the next session's manual smoke.

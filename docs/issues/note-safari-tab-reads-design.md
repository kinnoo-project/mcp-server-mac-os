**note**

Unit 13 of the capability-expansion roadmap: a new `application-safari` tool with
two read-only operations — `list_tabs` and `current_tab` — that let the assistant
answer "what do I have open?" and "what page am I looking at?". This is the first
capability to drive Safari, so it introduces a new MCP tool (a new `category`) and
a new Automation permission surface. This note records the design choices.

## Why a new tool, and why AppleScript builtins

A new capability `category` in the registry is projected as a new domain tool by
the server (`internal/server/tools.go`), so `application-safari` appears alongside
`application-mail`, `application-notes`, etc. — no server code change, just the
manifest. Safari exposes its windows, tabs, and the active ("current") tab only
through its scripting dictionary; there is no CLI that reads them. So — like the
Mail, Notes, and Calendar reads — the work is a FIXED AppleScript program plus
result parsing, implemented as in-process builtins
(`internal/engine/builtins_safari.go`) through the hardened `runOsascript` seam.

## Operations

- `list_tabs` — every open tab's title + URL, grouped by window, across all
  windows or scoped to a single 1-based window index (`window`, optional int).
  Non-browsing windows (Settings, downloads) have no `tabs` property, so the
  script wraps the `tabs of theWin` read in a `try` that treats them as empty. A
  tab with no URL yet (Start Page / Favorites) coerces to "" via `_str`. Output is
  bounded by the shared `compactOutput` budget so a session with very many tabs
  can't saturate context. Read-only / low.
- `current_tab` — the front window's active tab (title + URL). Errors with a clear
  "no Safari windows are open" message the Go side reports plainly rather than as
  a permission hint. Read-only / low.

Both manifest descriptions flag that open URLs are private so a caller reaches for
them deliberately.

## Explicit non-goal: `do JavaScript`

Safari can run arbitrary JavaScript in a tab via `do JavaScript`, which would let
the assistant read full page CONTENT. That path is intentionally NOT implemented:
it is gated behind Safari's Develop-menu "Allow JavaScript from Apple Events"
setting, is effectively remote code execution against whatever page is loaded, and
is far outside the read-the-tab-list scope of this capability. These builtins read
only tab metadata (title + URL). This is recorded in the file header and the
manifest so a future edit doesn't quietly add it.

## Injection posture — no free-text surface

Neither operation takes a free-text parameter. `list_tabs` accepts only an
optional integer window index (validated positive; a zero/negative value is
refused before any `osascript` call) and `current_tab` takes nothing. An integer
cannot carry an injection payload, so there is no dash-leading value to neutralize
and — unlike the Mail/Notes reads — no `reviewedFreeTextBuiltins` entry is
required (the injection-sweep gate only demands one for free-text builtins). The
value still reaches the script strictly as DATA bound to `on run argv`, after the
`--` end-of-options terminator that `runOsascript` inserts unconditionally, so the
"data, never code" property holds structurally; `TestSafariScripts_UseOptionTerminator`
documents that even the integer flows through the terminator.

## TCC (Automation) permission

Safari is a new Automation target — no other capability drives it. A denied
Automation permission surfaces via `safariScriptError` as a plain "grant this app
access to Safari in System Settings → Privacy & Security → Automation" hint,
mirroring `mailScriptError`/`notesScriptError`.

## Tests & evals

Pure Go logic only — no test launches `osascript` or reads real tabs
(`builtins_safari_test.go`): `parseTabRows` (well-formed + malformed/non-numeric
skipping), `renderTabList` (per-window grouping and renumbering, placeholders), the
positive-window guard, and the `--`-terminator assertion. Evals: CI-safe routing
cases `list_tabs_routing` / `current_tab_routing` (selection-only — executing them
reads live URLs, so real-content checks live in the manual list) plus manual
`m_safari_list_tabs` / `m_safari_current_tab`.

## Live verification status

The installed MCP server binary predates this unit, so an in-session runevals pass
cannot route the new `application-safari` ops yet; wiring was verified via the unit
tests and the server's `ValidateBuilders` + tool-surface integration test (which now
asserts exactly 20 tools including `application-safari`). Rebuilt `bin/` for the next
session. The live read path (real Safari + Automation grant) is on the manual smoke
checklist.

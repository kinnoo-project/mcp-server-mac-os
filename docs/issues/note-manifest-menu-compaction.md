**note**

# Compacting manifest operation summaries and parameter descriptions

## Why
Each domain tool's description embeds the full operation menu (built by
`internal/server/menu.go` from the capability `summary` and each `ParamSpec.Description`).
The model sees only the domain tools, so this menu is the **only** runtime surface
where it learns what an operation and its parameters do — there is no separate
per-operation schema. As domains grew (Photos = 20 ops, filesystem = 52 params),
the rendered menu got large enough that clients truncated it when loading the tool
schema (symptom: "the full operation list was truncated" when loading Photos).

Note: this is unrelated to `engine.maxOutputBytes` (32 KB). That constant caps a
subprocess's stdout, not the tool description/menu. Raising it does nothing here.

## What changed
Rewrote the `summary` and parameter `description` strings across all 15 manifests
to be terse — one tight sentence per op, short phrases per param — while keeping
every fact the model needs to form a correct call. No code changed; `menu.go`
renders whatever the manifests contain. Structure (names, types, risk,
reversibility, builder, arg specs, enums, defaults, required) is byte-for-byte
unchanged; only human-readable text shrank. Rendered menu total dropped ~27%
(Photos ~48%, 11.3 KB → 5.4 KB).

## Editing principles applied (for future manifest authors)
- **Drop per-op boilerplate.** The AppleScript-backed ops each repeated "Drives X
  via AppleScript; the first use may prompt for Automation access" — ~1.5 KB of
  pure repetition in Photos alone. It does not help the model form a call (the OS
  permission prompt surfaces at execution regardless), so it was removed.
- **Drop "(passes -X)" flag notes.** The model sets the *param* (e.g. `long: true`),
  never the flag, so the underlying flag letter is noise. `menu.go` does not render
  `arg` anyway.
- **Keep load-bearing semantics.** Retained: id/uid provenance ("from list_X"),
  what undo restores vs. "no auto-undo — reverse by hand", `~` support, the
  dash-leading (`-`) injection guard wording, no-overwrite / parent-must-exist for
  move/copy, REPLACES semantics for set_keywords, the cellular→FaceTime call-method
  guidance, and "needs Full Disk Access" on Messages reads.
- **Never leave a param with an empty description** — terse, not absent.

## Follow-ups (not done here)
- The dominant remaining cost is the per-op summaries themselves; if a domain still
  exceeds a client's ceiling, splitting it into sub-tools (e.g. `photos-read` /
  `photos-organize`) is the next lever.
- The exact client truncation ceiling was never pinned down; the compaction was
  sized to cut the worst offenders well below the old totals rather than to a
  measured limit.

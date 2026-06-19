**note**

The system-control work added a third execution lane to the server, alongside
"read-only runs immediately" and "mutating is staged behind the execute token":
an **auto-commit** lane for benign, frequent side-effects (launching/focusing an
app, opening a System Settings pane). Forcing the full stage → execute → undo
token dance on "open Notes" would be pure friction.

Design:
- A capability opts in with `auto_commit: true` in its manifest
  (`registry.Capability.AutoCommit`).
- The registry confines it to low-stakes mutations: `auto_commit` is rejected on
  a read-only capability (nothing to commit) and on any capability whose risk is
  `medium`/`high` (those must keep the human-approval gate). So paper-consuming
  prints (`print_file`, `print_test_page`) and lossy quits (`quit_application`)
  stay staged.
- The server still calls the engine's `Stage` step, so the forward and inverse
  commands are computed exactly as a gated mutation's are; it then runs the
  forward command immediately and, when an inverse exists, returns an `undo_`
  token in the same response (see `server.autoCommitMutation`). The reversibility
  guarantee ("what runs is what was staged") is preserved.
- Each operation line in a domain tool's menu states its lane ("runs
  immediately", "runs immediately; reversible via undo", or "STAGED — confirm
  with the user, then execute") via `server.executionLane`.

Today's auto-commit operations: `open_application`, `focus_application` (the
`application` domain) and `open_settings` (the `system` domain).

**Test-page scratch file trade-off:** macOS ships no CUPS test page, so
`print_test_page` embeds its own page (`internal/engine/testpage.txt`,
`//go:embed`) and, at stage time, writes it to `/tmp/mcp-fallback/testpage.txt`
(the staging directory blessed by `.claude/rules/transactional-state.md` §3). This
is a deliberate, narrow exception to "staging performs no side effects": it
touches only a server-owned scratch file, never any user or system state, and the
actual print still waits for `execute`. The alternative — generating the file at
execute time — isn't possible because a committed plan is just a pre-built
`Command` (binary + argv) with no place to run arbitrary Go.

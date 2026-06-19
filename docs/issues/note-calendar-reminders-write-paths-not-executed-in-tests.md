**note**

The Calendar/Reminders mutators (`add_event`, `modify_event`, `delete_event`,
`add_reminder`, `modify_reminder`, `complete_reminder`, `delete_reminder`) are
covered by unit tests that assert argv construction, the `--` option-injection
terminator, forward/inverse script selection, validation/rejection paths, and
preview text — but **no test ever executes a `StagedPlan.Forward` or
`Inverse`**. This mirrors the existing `send_mail` safety posture: executing
these would mutate the developer's real Calendar/Reminders (and, for the modify/
delete/complete operations, the stage step itself runs a live AppleScript probe
that needs a real event/uid and a granted Automation permission). So those
operations are tested only on the paths that reach a decision *before* the live
probe.

The hand-written AppleScript that is genuinely new and non-obvious — the shared
`_mkdate` / `_fmt` / `_pad` / `_str` / `_clean` date/format/escaping handlers in
`applescript.go` (`asDateHelpers`) — was validated by running it in isolation
through `osascript` (no `tell application`, so no app access or TCC prompt):
dates round-trip through `_mkdate`→`_fmt`, the end-of-day `+59` boundary lands
correctly, `_clean` flattens embedded tabs/newlines, and `_str` turns a
`missing value` into "". The per-operation `tell application "Calendar"/
"Reminders"` blocks use only standard, documented dictionary verbs.

**How to smoke-test the write paths manually (safe by construction):** drive
each operation through the normal MCP flow. Stage returns a preview + token and
changes nothing; `execute` applies it; `undo` reverses it. Because every
calendar/reminder mutation is reversible, a manual round trip
(stage → execute → verify in the app → undo → verify it reverted) exercises the
real AppleScript without risk. This is left as a manual step rather than an
automated test precisely because automating it would require mutating real
user data.

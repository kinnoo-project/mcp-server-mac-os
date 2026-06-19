# PR #8 review — Added Calendar and Reminders capabilities

2026-06-19, mode: fix

Copilot raised three comments — two real Reminders all-day-due-date bugs and one
doc-drift fix. All three were correct and are addressed.

---

(internal/engine/builtins_reminders.go) `list_reminders` only checks `due date`
when building output. All-day reminders use `allday due date`, so they show an
empty due field even though they are due.

**fixed**
`listRemindersScript`'s due block now checks `allday due date` first and falls
back to `due date` — the same precedence `probeReminderScript` (a few lines
down, line ~137) already used. The two reminder reads were inconsistent: the
probe handled all-day correctly, the list did not. They now match.

---

(internal/engine/mutate_reminders.go) In `modifyReminderScript`, switching a
reminder to an all-day due date clears `due date` but leaves any existing
`remind me date`, so a reminder that previously had a timed due date keeps a
stale alert after becoming all-day.

**fixed**
The "allday" branch now also `set remind me date of r to missing value`, exactly
as the "none" branch already does, keeping the three due-state shapes
(none / allday / timed) internally consistent — only "timed" should leave a
`remind me date` set.

Note on tests: both fixes are inside fixed AppleScript constants whose behavior
only manifests when run against the real Reminders app. Per this project's
established posture (see docs/TESTS.md safety notes), no test executes these
scripts, so neither fix is covered by an automated assertion that would have
caught it — the same reason the original gaps slipped through. The corrections
are verified by review and by making each branch identical to an
already-reviewed sibling (`probeReminderScript`'s due precedence; the "none"
branch's `remind me date` clearing). Manual smoke-testing remains the way to
exercise these paths end-to-end (stage → execute → undo).

---

(docs/TESTS.md) The documented `stageSendMail` argv layout omits the `--`
terminator, but send_mail is hardened with `--` to prevent osascript option
injection.

**fixed**
Updated the documented layout to `["-e", script, "--", subject, body,
recipientCount, recipients..., attachments...]` and noted that the `--` is the
end-of-options terminator that blocks option injection. This was prose drift —
the code and its tests already carry the `--` (added in PR #7); only this
description had not been updated.

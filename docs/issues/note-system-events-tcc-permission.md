**note**

`set_appearance` (Unit 8) is the first capability to drive **System Events** —
macOS's GUI-scripting bridge — as a mutation. It reads and sets the system-wide
Dark/Light appearance through System Events' `appearance preferences` via
`osascript`. The first such call triggers a one-time macOS Automation (TCC)
prompt asking the user to let the host process control System Events; until
granted, the script exits non-zero.

This is handled gracefully by the shared `systemEventsScriptError` helper
(`internal/engine/mutate_preferences.go`), which detects the non-zero exit and
returns a message pointing at System Settings → Privacy & Security → Automation,
rather than a bare AppleScript error. The helper is surfaced at **stage time**:
set_appearance probes the current appearance via System Events to build its
inverse, so a missing grant is reported before anything is committed (and since
set_appearance is auto_commit, "before commit" is the only moment that matters).

**"System Events" Automation is distinct from per-app Automation.** Granting the
host app control of, say, Mail or Contacts does not grant control of System
Events — it appears as its own entry under the host app in the Automation list.
This same grant (and the same `systemEventsScriptError` mapping) will be reused
by window management (Unit 11), which also reads/moves windows through System
Events. Note that Unit 11's window *reads* additionally require the separate
**Accessibility** permission (a different TCC surface again); appearance
switching does not.

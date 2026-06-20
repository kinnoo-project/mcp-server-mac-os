**note**

The `application-calendar` and `application-reminders` capabilities drive
Calendar.app and Reminders.app through their AppleScript dictionaries via
`osascript`. The first time any of them runs, macOS shows a one-time
Transparency/Consent/Control (TCC) "Automation" prompt asking the user to let
the host process (Terminal, the MCP client, etc.) control Calendar/Reminders.
Until that grant is given, the script exits non-zero.

This is handled gracefully rather than papered over: `calendarScriptError` /
`remindersScriptError` (in `builtins_calendar.go` / `builtins_reminders.go`)
detect the non-zero exit and return an actionable message pointing the user at
System Settings → Privacy & Security → Automation, instead of surfacing a bare
AppleScript error number. It is the same UX pattern as Full-Disk-Access for the
filesystem capabilities, just a different TCC category (Apple Events).

A second, performance-related consequence: a `query_events` call issues a
`whose start date ≥ … and ≤ …` query against every (or one named) calendar,
which Calendar.app can take a few seconds to answer on a busy account. The
`maxEventLimit` cap bounds how much is requested and returned; the operation's
summary warns the model that the call may be slow.

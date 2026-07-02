**note**

Unit 6 of the capability-expansion roadmap (`~/.claude/plans/woolly-noodling-stream.md`)
adds two agent→human back-channels to the existing `system` domain (no new MCP
tool): `notify` (a Notification Center banner) and `speak` (text-to-speech via
`say`). Both let a long-running or background task get the user's attention
without them watching the transcript.

Design choices worth recording:

- **Both are irreversible and auto_commit.** A posted banner cannot be recalled
  and played audio cannot be unheard, so each `StagedPlan` carries `Inverse = nil`
  and its preview says the action cannot be undone. They are low-risk one-shot
  side effects, so forcing a stage→execute round-trip on "ping the user" would be
  pure friction — they run in the auto-commit lane (registry-enforced: auto_commit
  is only legal at risk none/low). The auto-commit lane already renders the
  "cannot be undone" text from the nil inverse, so no new server machinery.

- **notify uses the shared osascript seam.** The forward command is a FIXED
  `display notification (item 1 of argv) with title (item 2 of argv)` script run
  through `osascriptCommand`, so the message and title arrive as data bound to
  `on run argv` after the `--` end-of-options terminator — the same
  option-injection guard every AppleScript-backed capability inherits (CLAUDE.md
  §4 / applescript.go). No Automation (Apple Events) grant is needed to post a
  notification; whether the banner is *shown* is governed by the HOST MCP CLIENT's
  own Notifications permission (System Settings → Notifications), which the tool
  description points the user at.

- **speak uses `say` with a `--` terminator.** `say` (at `/usr/bin/say`, a trusted
  dir) honours `--` — verified: `say -- "-v Alex"` speaks the literal text, while
  `say "-v"` parses `-v` as its voice flag. So the text is passed as a single
  positional operand after `--`, making a dash-leading value (`-v`, `-r`,
  `--file-format`) inert data rather than a flag. This mirrors the osascript `--`
  discipline rather than the mdfind-style dash-rejection (mdfind has no `--`).

- **Caps.** notify bounds message/title (1000/200 bytes) only to keep the staged
  plan and preview small — Notification Center truncates long banners anyway.
  speak caps text at 2000 *characters* (counted as runes, not bytes, so the limit
  matches the human notion of length) so speech stays well under the engine's
  2-minute subprocess kill; a cancelled request (client drops the socket) stops
  playback via the context binding.

- **Evals are manual.** Because both ops are auto_commit, invoking the tool
  commits the side effect immediately — there is no CI-safe selection-only call
  the way a staged mutation has (a single un-confirmed turn). So routing is
  verified by manual cases (`m_system_notify_banner`, `m_system_speak_aloud` in
  `evals/cases/manual_smoke.json`), matching how every other side-effecting
  auto_commit op in this repo (open_application, open_settings, write_clipboard)
  is verified. The plan's "routing A" was written before this auto_commit
  constraint was pinned down; M is the correct classification here.

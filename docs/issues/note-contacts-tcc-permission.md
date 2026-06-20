**note**

`find_contact` (and the `call` capability's resolution of a `contact_name` into
a number) reads Contacts.app through its AppleScript dictionary via `osascript`.
The first such call triggers a one-time macOS Automation (TCC) prompt asking the
user to let the host process control Contacts; until granted, the script exits
non-zero. This is handled gracefully: `phoneScriptError`
(`internal/engine/builtins_phone.go`) detects the non-zero exit and returns a
message pointing at System Settings → Privacy & Security → Automation, rather
than a bare AppleScript error.

Placing the call itself (`call`) uses `/usr/bin/open` with a `tel:`/`facetime:`
URL, which launches the relevant handler app; that path does not need the
Automation permission (FaceTime/Phone present their own call UI). Only the
Contacts *lookup* is gated by TCC. Same pattern as the Calendar/Reminders
automation prompt — see
`docs/issues/note-calendar-reminders-tcc-permission.md`.

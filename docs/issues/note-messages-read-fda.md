**note**

The `application-messages` read operations (`check_messages`,
`search_messages`, `read_conversation`, `list_conversations`) query the local
Messages database at `~/Library/Messages/chat.db` using
`sqlite3 -readonly -json`. That database is protected by macOS, so the host
process must be granted **Full Disk Access** (System Settings → Privacy &
Security → Full Disk Access). Until then, sqlite3 fails to open the file with
"authorization denied" / "unable to open database"; `messagesDBError`
(`internal/engine/builtins_messages.go`) detects exactly those strings and
returns an actionable hint instead of a bare sqlite error.

This is a heavier permission than the Automation (Apple Events) grant the other
app domains use — it is the trade-off for reading message history at all, since
Messages.app's AppleScript dictionary offers no usable read access. Sending
(`send_message`) uses AppleScript and needs only the Automation grant, not FDA.

Design posture for the reads (recorded for future maintainers):
- The DB is opened `-readonly` — a read capability can never write, even by bug.
- `-json` output is decoded with stdlib `encoding/json`, so message bodies
  containing tabs/newlines/quotes cannot corrupt parsing (no new dependency).
- Every query is a fixed template; the only variable pieces are a numeric LIMIT
  (Go `int` via `%d`) and a validated/escaped handle or search term — see the
  "No SQL injection" point in the README and `escapeSQLLiteral`.
- Apple stores message timestamps as nanoseconds since 2001-01-01 UTC; the
  conversion to local time (`/1000000000 + 978307200`, `datetime(...,'unixepoch',
  'localtime')`) is done in SQL so Go needs no date math.

Message text recovery: recent macOS stores most message text only in the
`message.attributedBody` blob (a serialized NSAttributedString "typedstream"),
leaving `message.text` NULL. The reads fetch `hex(attributedBody)` when `text`
is empty and recover the string in Go (`extractTypedstreamText` /
`decodeAttributedBody`): anchor on the `NSString` class marker, find the `+`
(0x2B) inline-data marker a few bytes later, read the typedstream length, then
the UTF-8 text. This was verified against real chat.db rows (e.g. a blank-`text`
message whose blob reads `…NSString 01 94 84 01 2B 40 <utf8…>`). It is
best-effort: an unrecognized blob shape degrades to the "(no text — attachment
or unsupported message)" placeholder rather than erroring the read.

Remaining limitation: genuinely text-free messages (image/file attachments,
tapbacks, or unusual rich payloads my extractor doesn't recognize) still render
as that placeholder. Full typedstream deserialization (attributes, multiple
runs) is a documented fast-follow, not built now.

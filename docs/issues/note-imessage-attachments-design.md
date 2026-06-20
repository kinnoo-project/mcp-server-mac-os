**note**

Design decisions made while adding file-attachment support to `send_message`
(`internal/engine/mutate_messages.go`), beyond what's in README.md's
`send_message` section. These mirror the `send_mail` attachment work — see
`note-send-mail-attachments-design.md` — but a few points are specific to
Messages.

1. **Count-prefixed argv layout, same as `send_mail`.** The fixed
   `sendIMessageScript` receives `[handle, text, attachmentCount, paths...]`
   as `--`-terminated `on run argv` data. argv item 3 is the attachment count
   as a string; AppleScript slices `items 4 thru (3+N)` for the paths. No
   delimiter character that a path might legitimately contain, no ambiguity,
   and it matches the pattern already reviewed for mail. (Messages only has
   one variable-length list — attachments — so a count isn't strictly
   required here, but using the same shape keeps the two send paths legible
   side by side and leaves room if a second list is ever added.)

2. **`text` became optional; "text OR attachments" is enforced in Go, not the
   manifest.** A file may be sent with no caption, so `text` is no longer
   `required: true` in `messages.json`. The engine's required-param check runs
   before the stage function, so a conditional "one of these two" rule can't
   live in the manifest — `stageSendMessage` rejects the empty/empty case
   itself (`provide 'text', 'attachments', or both`).

3. **Attachments are sent first, then the text.** Each `send` is a separate
   iMessage — AppleScript's Messages dictionary has no single
   "image-with-caption" send — so the order is a deliberate choice: the
   attachment(s) go out first, then any text, mirroring how the Messages UI
   stacks an image above its caption. With no text, only the file(s) are sent.

4. **Attachment paths are validated at stage time** (`os.Stat`, reject missing
   paths and directories) — the same read-before-stage discipline `mkdir` and
   `send_mail` use. This also guarantees the script's `(POSIX file …) as alias`
   coercion, which errors on a nonexistent file, won't fail mid-send at commit
   time. The `attachments` manifest param is typed `path_list`, so `~`-relative
   paths (e.g. `~/Downloads/Leah.png`) are tilde-expanded by the engine before
   the stage function ever sees them.

5. **The exact `osascript` send-attachment syntax is version-sensitive and
   verified by manual smoke test, not automated execution** — sending a real
   iMessage is irreversible, so no unit test runs the `Forward` command (it
   stops at asserting the staged argv/preview). See
   `note-imessage-applescript-send.md` for the standing caveat that Messages'
   scripting `send` has varied in reliability across macOS releases; the
   `(POSIX file path) as alias` → `send … to targetBuddy` form is the attachment
   analogue and carries the same "confirm on real hardware" expectation.

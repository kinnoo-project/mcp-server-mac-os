**note**

Design decisions made while adding attachment support to `send_mail`
(`internal/engine/mutate_mail.go`), beyond what's in README.md's `send_mail`
section.

1. **Count-prefixed argv layout, not a delimiter.** `send_mail` now has two
   variable-length lists (recipients, attachments) sharing one flat argv
   after `osascript -e <script>`. A delimiter character (e.g. a fixed
   separator string between the two lists) would risk collision with a
   legitimate value — unlikely for an email address, but a delimiter-based
   scheme is fragile by construction. Instead, argv item 3 is the recipient
   count as a string; AppleScript slices `items 4 thru (3+N)` for recipients
   and everything after that for attachments. No delimiter, no ambiguity,
   and it generalizes if a third variable-length list is ever needed (just
   add another count).

2. **"Find the file, then attach it" does NOT need the `pipeline` tool —
   and structurally can't.** This was a direct question this session: doesn't
   "find my tax return and email it" need `pipeline` to chain `find` into
   `send_mail`? No — `pipeline` only admits read-only, binary-backed stages
   (`SupportsPipeline` in `internal/engine/pipeline.go`); `send_mail` is a
   mutator and is permanently ineligible, by design (nothing to roll back).
   The actual mechanism is the ordinary one every multi-step request already
   uses: the model calls `filesystem.find`, reads the path out of that
   call's result, then calls `application-mail.send_mail` with
   `attachments: [<that path>]` as a second, separate tool call. No new
   engine work was needed for this once `attachments` existed as a
   parameter — it was already possible the moment `send_mail` could accept a
   path.

3. **Attachment paths are validated at stage time** (`os.Stat`, reject
   missing paths and directories) — the same read-before-stage discipline
   `mkdir` uses for its target path. Catches a typo'd path before showing a
   preview, rather than discovering it only when `osascript` fails mid-send.
   Directories are rejected outright rather than attempted, since Mail's
   `make new attachment` doesn't have an obvious "attach this whole folder"
   behavior worth relying on.

4. **The exact `osascript` "make new attachment" syntax has not been
   empirically verified against a real send in this session.** Verifying it
   would mean actually automating the user's real Mail.app (the one-time
   Automation permission prompt, a real `tell application "Mail"` call) —
   a real side effect that wasn't authorized mid-implementation. The syntax
   used (`make new attachment with properties {file name:(POSIX file
   attPath)} at after the last paragraph`) is the standard, widely-documented
   idiom, but **the user should do the first live attachment send themselves**
   rather than assume it's proven. If it's wrong, `osascript` will fail with
   a real AppleScript runtime error surfaced through the normal error path
   (not a silent failure), so a syntax problem would be loud, not subtle.

**status: implemented (attachments); forward/reply still open, see
`docs/issues/note-applescript-mail-search-deferred.md`'s "future upgrade
path" note for the related message-id gap they'd also need.**

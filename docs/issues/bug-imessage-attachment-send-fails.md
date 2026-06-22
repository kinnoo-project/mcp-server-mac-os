**bug**

`send_message` with an `attachments` path failed to actually deliver the file on
modern macOS (reproduced on macOS 15.6 Sequoia; the same mechanism applies to
Sonoma 14). The message text sent fine, but any file attachment showed up in the
Messages conversation and then stuck as "Not Delivered" / "failed to send." Both
a small (~800 KB) image and a ~5 MB PDF failed, so it was NOT a size limit.

Root cause (diagnosed from the Messages database, `~/Library/Messages/chat.db`):
Messages.app runs inside the macOS App Sandbox. When `send_message` hands it an
attachment path via AppleScript (`send (POSIX file …) as alias to buddy`), it is
Messages — not this server — that must open and upload the file. A path OUTSIDE
Messages' sandbox (e.g. `~/Downloads`, `/tmp`) is unreadable to the sandboxed
app, so the upload fails. The failure is invisible to AppleScript (the `send`
call returns without error); it only shows up in the database as the attachment's
`transfer_state = 6` (transfer failed) and the message's `error = 3`.

The signal was unambiguous. Across the full outgoing-attachment history, every
send split on ONE axis — where the file lived at send time:

| File location at send | `transfer_state` | Result        |
| --------------------- | ---------------- | ------------- |
| External (`~/Downloads`, `/tmp`) | 6     | failed, every time |
| Inside Messages' store (`~/Library/Messages/Attachments/…`) | 5 | delivered, every time |

The "store" successes were files Messages had itself copied in via the UI
(drag/paste), which grants the app read access. AppleScript sends got no such
grant. Confirmed by copying the SAME file that failed from `~/Downloads` into a
path inside Messages' sandbox and re-sending: it uploaded cleanly
(`transfer_state = 5, is_sent = 1`). Chat-vs-buddy targeting made no difference;
text-only sends were never affected (no file to read).

Two earlier notes had flagged the `send` path as version-sensitive but treated it
as an opaque "Messages automation is flaky across releases" risk
(`note-imessage-applescript-send.md`, `note-imessage-attachments-design.md`).
This bug pins the actual mechanism: it is not the `send` verb, it is sandbox file
access.

**fixed**

The fix is a file-location change, not an AppleScript change. Before sending,
each attachment is copied into a directory inside Messages' OWN sandbox container
— `~/Library/Containers/com.apple.MobileSMS/Data/tmp/mcp-send/<id>/<i>/<name>` —
so the app is guaranteed read access, and the existing `send … to buddy` script
then uploads it normally. Implemented in
`internal/engine/messages_sandbox.go` (`stageAttachmentsIntoSandbox`) and wired
into `stageSendMessage` (`internal/engine/mutate_messages.go`); text-only sends
skip it entirely.

Details:
- The copy happens at STAGE time, because the engine runs a staged `Forward` as a
  single subprocess with no in-process step between the user's confirmation and
  the AppleScript firing. The copy is harmless scratch (nothing is sent until
  `execute`); the preview still shows the original filenames.
- Copies are not deleted in the same operation — the upload Messages performs
  after `send` is asynchronous and still needs the file. Instead, each later send
  sweeps `mcp-send/*` staging dirs older than one hour
  (`sweepStaleStagingDirs`), bounding disk use without pulling a file out from
  under an in-flight upload.
- Same-basename attachments are isolated in per-index subdirectories so neither
  overwrites the other while keeping the recipient-visible filename.

Hardcoded assumption: Messages.app's bundle id `com.apple.MobileSMS` (stable
across many releases) locates the sandbox container. If it ever changes, the
container path is absent and an attachment send is refused up front with a clear
error rather than silently failing the way this bug did; text sends are
unaffected. See the `messages_sandbox.go` file header for the full rationale.

No automated test sends a real message (irreversible). Tests assert the staged
argv points at the sandbox copy (bytes/basename preserved), the absent-container
rejection, and the sweep. Delivery is a manual smoke test verified via
`transfer_state = 5` — see `docs/TESTS.md`.

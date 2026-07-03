**note**

Unit 10 of the capability-expansion roadmap: two read-only Mail.app operations —
`list_inbox` and `read_message` — that close the everyday "read me that email"
hole. Mail previously offered only `search_mail` (Spotlight full-text search) and
`send_mail` (compose+send). This note records the design choices.

## Why AppleScript builtins, not Spotlight/sqlite

`search_mail` answers "find messages matching a term" against Spotlight's index
of the on-disk store, but it cannot list "the newest messages in my inbox"
(there is no query term) nor return a specific message's body by identity. Mail's
own scripting dictionary is the supported way to enumerate a mailbox and fetch a
message by id, so — like the Notes and Calendar reads — the work is a FIXED
AppleScript program plus result parsing, implemented as in-process builtins
(`internal/engine/builtins_mail_reads.go`) rather than generic argv builders.
Mail has no documented, stable sqlite schema to read directly (unlike Messages'
`chat.db`), which is the other reason the dictionary is the right seam.

## Operations

- `list_inbox` — most recent N messages in the unified Inbox (or a named
  mailbox): id, sender, subject, date received, one `_clean`-flattened row each.
  N defaults to 20, hard-capped at 50 (`cappedInboxLimit`). Read-only / low.
- `read_message` — one message's full plain-text body plus From/Subject/Date, by
  the id from `list_inbox`. Output bounded by the shared `compactOutput` budget so
  a long email can't saturate context. Read-only / low; the manifest description
  flags that it returns real personal mail so a caller invokes it deliberately.

## Message ordering (the one soft assumption)

Mail returns `messages of <mailbox>` in the mailbox's date order, newest first,
so the script reads items 1..N — the most recent N — WITHOUT scanning the entire
(potentially huge) mailbox. The Go side additionally re-sorts the returned rows
newest-first (`sortInboxNewestFirst`, a descending lexicographic sort of the
"YYYY-MM-DD HH:MM" field) as a stable-display safety net. If a user has changed
Mail's default sort so the dictionary yields a different order, the "top N" could
differ from the true newest N; this is a known soft edge, acceptable for a
convenience read and cheap relative to enumerating a large mailbox. Documented
here rather than hidden.

## read_message id resolution

A Mail message `id` is unique, but a message can live in the Inbox and/or an
account mailbox. `read_message` tries the unified Inbox first (the common,
cheapest case), then falls back to scanning every account's mailboxes with a
native `whose id is …` filter, so a message filed outside the inbox is still
readable. A non-matching id makes the script `error "no message with that id"`,
which `runReadMessage` turns into a clear "use list_inbox to get a current id"
message — distinct from a permission failure.

## Security / injection

Both ops drive Mail through the hardened `runOsascript` seam, which inserts the
`--` end-of-options terminator before any data value, so a model-supplied mailbox
name or id (e.g. `-e`) reaches the fixed script strictly as `on run argv` data,
never as an osascript flag. The scripts are FIXED Go constants; nothing is
concatenated into the source. Both are listed in `reviewedFreeTextBuiltins`
(`injection_sweep_test.go`) pointing at `builtins_mail_reads_test.go`.

As a small hardening refactor, the argv assembly shared by the mutating path
(`osascriptCommand`) and the read path (`runOsascript`) was extracted into one
`osascriptArgv` helper in `applescript.go`, so the "`--` is always present"
guarantee is structural and unit-tested in one place
(`TestOsascriptArgv_SharedByBothPaths`) rather than duplicated at two call sites.

## TCC / permissions

Driving Mail via AppleScript is an Automation permission surface already
established by `send_mail`. `mailScriptError` maps a denied grant to an
actionable "grant this app access to Mail in System Settings → Privacy & Security
→ Automation" hint, mirroring `notesScriptError`/`calendarScriptError`.

## Evals

Both are inherently non-CI-safe: any live inbox read surfaces real mail content
to the API, so the two cases are `manual: true` in `manual_smoke.json`
(`m_mail_list_inbox_readonly`, `m_mail_read_message`). They validate structurally
via the evals loader test; live smoke against a real mailbox is a deliberate
human step. Pure parse/render/limit/guard logic is covered without touching Mail
in `builtins_mail_reads_test.go`.

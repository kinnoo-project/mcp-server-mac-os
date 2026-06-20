**note**

`search_mail` (`internal/engine/builtins_mail.go`) uses `mdfind`/`mdls`
(Spotlight) for v1, not Mail.app's own AppleScript `whose`-clause search.
This was a deliberate choice, not an oversight — recorded here so the
tradeoff and the upgrade path aren't re-derived from scratch later.

**Why Spotlight for v1:** no Automation/TCC permission needed (a different,
lower-friction permission model than Apple Events), and it's structurally
identical to the existing `find`/`grep` capabilities — a trusted binary,
explicit argv, read-only, no AppleScript-injection surface to reason about
at all.

**Where Spotlight is less precise than asking Mail.app directly, and why:**
- **Brand-new mail**: Spotlight indexes mail asynchronously in the
  background, separately from Mail.app's live state. A message that just
  arrived may not be in the index yet, especially under load. AppleScript
  talking to Mail.app directly sees its in-memory state immediately.
- **Old or recently-migrated mail**: after a new Mac setup, account
  re-add, or backup restore, Spotlight has to re-index the whole mail
  backlog from scratch — for a large archive this can take a long time,
  during which old mail is invisible to `mdfind` even though Mail.app
  shows it fine.
- **Junk/Spam**: commonly excluded from Spotlight's index by design
  (privacy/spam-isolation reasoning), so a message visible in Mail.app's
  Junk mailbox may simply not be findable via `mdfind`.
- **Account/mailbox-exact scoping**: AppleScript can say `mailbox "Inbox"
  of account "Work"` by name. `mdfind` can only scope with `-onlyin
  <path>`, and Mail's on-disk layout uses opaque per-account UUID
  directories — there's no clean way to express "only my Work account"
  without first resolving that name to a filesystem path some other way.
- **Live state (read/flagged status)**: these are mutable UI states that
  Spotlight's metadata snapshot may not track reliably; AppleScript reads
  Mail.app's actual current state directly.

**The tradeoff going the other way:** AppleScript's `whose`-clause search
evaluates live data by walking the mailbox, which is slow on a large
mailbox (thousands of messages). Spotlight's precomputed index is much
faster for broad "search everything" queries. So this isn't a strict
upgrade either way — it's "Spotlight for the common case, AppleScript for
when you need account/mailbox-exact or recency-sensitive results."

**Future upgrade path:** add an AppleScript-backed search (mirroring
`send_mail`'s fixed-template-via-`osascript` pattern in
`internal/engine/mutate_mail.go`, but read-only) as either a second
capability (e.g. `search_mail_precise`) or a `scope`/`account` parameter on
`search_mail` that switches implementation under the hood. Not built now —
no concrete need has surfaced yet, and it adds the Automation-permission
dependency `search_mail` currently avoids entirely.

**status: open — documented future option, not built**

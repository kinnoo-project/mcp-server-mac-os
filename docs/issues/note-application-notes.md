**note**

The `application-notes` domain adds six capabilities driving Notes.app through
its AppleScript dictionary via `osascript`: four reads (`list_notes`,
`search_notes`, `read_note`, `list_folders`) and two reversible writes
(`create_note`, `append_to_note`). Reads are builtins in `builtins_notes.go`;
writes are staged mutators in `mutate_notes.go`. As with Calendar/Reminders, the
first use triggers a one-time TCC "Automation" prompt; `notesScriptError` turns a
denial into an actionable System-Settings hint (same pattern as
`calendarScriptError`).

Design choices worth recording:

- **Reads use the `plaintext` property, writes use `body` (HTML).** A note's
  content is HTML in `body`, but the dictionary also exposes a read-only
  `plaintext` property. Reads use `plaintext`, so there is zero HTML parsing in
  Go (cleaner than the Messages `attributedBody` decoding). Writes must set
  `body`, so the mutators build HTML in Go via `noteBodyHTML`/`appendedHTML`:
  model text is HTML-escaped (`html.EscapeString`) and newlines become `<br>`, so
  a literal `<`, `>`, or `&` renders as text and cannot inject markup.

- **First line is the title — verify against the live app.** Notes derives a
  note's displayed title from the first line of its `body`, so `create_note`
  emits the title as a bold first `<div>`. The exact behavior (and whether the
  `name` property is independently settable) has varied across macOS versions; the
  composition was chosen for the Ventura+ floor and should be re-confirmed against
  the live app if a future macOS changes it. The unit tests assert the *Go-built
  HTML string*, not what Notes renders from it.

- **`create_note`'s inverse deletes by title, not by id.** Both forward and
  inverse commands are resolved at stage time (see `mutate.go`), but a new note's
  internal id does not exist until commit. So — exactly like `add_reminder`'s
  inverse — the compensating delete matches on title (`deleteNoteByTitleScript`),
  removing only the first match. Known caveat: if another note with the same title
  already exists, undo could remove that one instead. This is the same tradeoff
  already accepted for reminders and is surfaced in the create preview text.

- **Locked notes degrade, never error.** A password-protected, locked note errors
  on `plaintext`/`body` access. The read scripts wrap per-note text access in
  AppleScript `try` so a locked note lists on its (readable) title and reads back
  as "(locked note)" rather than failing the whole operation. `append_to_note`
  refuses to stage against a note whose body cannot be read (a locked note),
  because it could not build a faithful inverse.

**issue**

Folder targeting does not disambiguate across accounts. `create_note`/`list_notes`
resolve a folder with `first folder whose name is …`, so if the same folder name
exists under more than one account (e.g. iCloud *and* On My Mac), the first match
wins. `list_folders` lists names only, without their owning account. Multi-account
folder disambiguation is deferred; for now a note lands in whichever same-named
folder Notes returns first, or the default folder when none is given.

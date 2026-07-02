**note**
Design choices for `filesystem.write_file` / `filesystem.append_to_file` (U1 of
the capability roadmap):

- **`tee` as the writer.** The forward commands are `tee -- <path>` and
  `tee -a -- <path>` with the content on `Command.Stdin` — a pure data sink, so
  model-controlled content can never be parsed as flags, paths, or code (see
  `docs/issues/note-engine-stdin-commands.md`). `tee` echoes what it writes to
  stdout; that echo flows back through the normal output compaction (32 KB
  head/tail), which is harmless and gives the model confirmation of what
  landed.
- **write_file is create-only.** Staging refuses an occupied path — including a
  dangling symlink, checked with `Lstat`, since writing through one would create
  a file somewhere the preview never named. Overwrite-by-intent is expressed as
  `remove` (Trash) followed by `write_file`, keeping every step reversible. The
  undo moves the created file to the Trash (`compensatable`), matching the
  recycling rule.
- **append_to_file's undo is a snapshot restore.** Staging reads the file's
  current bytes and bakes them into a truncating `tee` inverse, so undo is
  byte-exact — and deliberately discards any edits made between execute and
  undo, which the preview states plainly. To bound the staged payload the
  target must be a regular file ≤ 8 MiB; content is capped at 1 MiB
  (`maxWriteContentBytes`), both well under the engine-wide 16 MiB stdin
  backstop.
- **Empty write_file content is allowed** (creates an empty file);
  empty append content is rejected (a no-op that would stage a pointless undo
  payload).

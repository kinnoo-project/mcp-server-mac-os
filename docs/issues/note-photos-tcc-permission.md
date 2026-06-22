**note**

The `application-photos` capabilities drive Photos.app through its AppleScript
dictionary via `osascript`. The first time any of them runs, macOS shows a
one-time Transparency/Consent/Control (TCC) "Automation" prompt asking the user
to let the host process (Terminal, the MCP client, etc.) control Photos. Until
that grant is given, the script exits non-zero.

This is handled gracefully rather than papered over: `photosScriptError` (in
`builtins_photos.go`) detects the non-zero exit and returns an actionable message
pointing the user at System Settings → Privacy & Security → Automation, instead of
surfacing a bare AppleScript error number. It is the same UX pattern as the
Calendar/Reminders/Notes capabilities, just for Photos.

**Capability-shape consequences of the Photos dictionary**

The Photos scripting dictionary (`/System/Applications/Photos.app/Contents/
Resources/Photos.sdef`, read on macOS 26 / Photos 10.0) constrains what the
domain can offer, and those constraints are load-bearing safety properties:

- **No photo deletion is possible.** The `delete` verb applies only to albums and
  folders, never to a media item. So no operation in this domain can delete a
  photo or video — the most destructive thing a user might fear is simply not
  reachable through the supported API. (We do not work around this with UI/System
  Events scripting; that would violate the project's "data, never code" axioms and
  be fragile across OS versions.) Album/folder deletion *is* expressible, but is
  intentionally **not** exposed: low value-add, and an accidental destructive call
  outweighs it.

- **Several writes have no scripted inverse.** There is no command to remove a
  media item from an album, and none to delete an imported item. So `create_album`,
  `create_folder`, `add_to_album`, and `import_photos` stage with `Inverse == nil`
  and a preview that states plainly how to reverse them by hand (an imported item,
  if later deleted in Photos, lands in Recently Deleted for 30 days). The cleanly
  reversible writes are the media-item property setters (`set_favorite`,
  `set_title`, `set_description`, `set_date`, `set_keywords`), which capture the
  prior value at stage time and restore it on undo.

- **No people/faces or smart-album criteria.** Those classes are absent from the
  dictionary, so "find photos of <person>" is not directly expressible. The native
  `search` command (exposed as `search_photos`) is the broad discovery path: it is
  the same index as the in-app Search field, covering scenes, places, dates, and
  text — far more than filename/keyword matching.

**Performance & privacy**

Read listings never enumerate the whole library: each touches only `media items 1
thru N`, capped by `maxPhotoLimit` (100), so a large library cannot make a listing
slow or saturate the model's context. GPS coordinates are sensitive and are
returned only by the single-item `get_photo`, never in bulk listings.
`export_photo` (the only path that copies pixels out of the library) writes into a
fresh, uniquely-named directory so it can never overwrite an existing file,
keeping it in the no-confirmation read-only lane like `capture_screen`.

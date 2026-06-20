**note**

`open_file` (application domain) opens a file in a named app and, while staging,
decides whether the app actually supports the file's type so the confirmation
preview can warn the user. Several deliberate design choices were made:

- **Always staged, never auto-commit.** Unlike `open_application`, every `open_file`
  waits behind the execute confirmation gate. The support check does not *block* —
  it only enriches the preview — so the single decision point is the human
  confirming (or not) at execute time. This is what satisfies the original "stop
  before opening an unsupported file" goal without needing a hard refusal or a
  `force`/override parameter.

- **`app` is optional; omitting it uses the default handler.** A prompt like "open
  this PDF" names no app, so `app` is optional. When it is absent the forward
  command is just `open -- <file>`, which opens the file in whatever app macOS has
  registered as the default for its type. That path runs no support check (the
  default handler opens the type by definition; if there is none, `open` errors at
  execute) and offers no undo (staging cannot know which app will launch, so there
  is nothing specific to quit). When `app` IS given, the full named-app behavior
  below applies. (Note: the named-app branch reads the `app` parameter — an earlier
  draft mistakenly reused the `name`-reading validator shared with
  open/focus/quit; `validateAppNameValue` now takes the value explicitly, and a
  regression test pins that the `app` param is the one read.)

- **`mdimport -t -d1` over `mdls` for the file's type.** `mdls` returns "could not
  find" on files the Spotlight index has not seen (verified on this machine for
  files in `~/Downloads`, `~/Desktop`, and the repo even with indexing enabled),
  which would make the check unreliable. `mdimport -t -d1` computes the type on
  demand (`-t` = test mode, never writes the index) and prints `... of type
  '<uti>'`, working regardless of indexing. Its debug line goes to stderr, so both
  streams are scanned.

- **App support read from the bundle's `Info.plist` via `plutil`.** We parse
  `CFBundleDocumentTypes[].CFBundleTypeExtensions` (extensions) and
  `[].LSItemContentTypes` (UTIs). No private Launch Services calls.

- **Match rule = extension OR exact UTI; no UTI conformance-tree walk.** An app
  supports a file when the file's extension is declared (or `*` is declared,
  meaning "any file") or the file's exact UTI is declared. We intentionally do NOT
  resolve UTI conformance (e.g. `net.daringfireball.markdown` conforming to
  `public.text`), because there is no Spotlight-free CLI for it and a strict result
  is safe here: it only adds a warning to a preview the user can still confirm. In
  practice `mdimport` reports the concrete `public.*`/`com.*` UTI that apps usually
  declare directly (verified: Preview matches `public.png`/`com.adobe.pdf`; a
  `.txt`/`public.plain-text` correctly does not match Preview), so the gap rarely
  bites.

- **Three-way verdict, not boolean.** supported → clean "Open X in Y" preview;
  unsupported (both sides read, no match) → ⚠️ warning listing what the app does
  handle; uncertain (app not located, app declares no document types, or the file's
  type could not be read) → ⚠️ "could not confirm support" warning. The verdict only
  shapes preview text; the staged forward/undo commands are identical in all cases.

- **Injection hardening.** The app name never enters the `mdfind` query (we run the
  fixed app-bundle query and match in Go, since `mdfind` has no `--` terminator);
  the file path is passed after `--` to `open`, `plutil`, and `mdimport`, and a
  dash-leading file is rejected up front. A regression test feeds a `-e` file/app
  and asserts rejection.

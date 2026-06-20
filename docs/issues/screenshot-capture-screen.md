**note**

The `screenshot` domain adds one capability, `capture_screen`, which photographs
the desktop with `/usr/sbin/screencapture` and returns where the image was saved
plus its pixel dimensions and byte size. Implementation: a read-only builtin
(`runCaptureScreen` in `builtins_screenshot.go`), registered like the other
read builtins in `builtins.go`. No policy change was needed — `/usr/sbin` is
already a trusted binary directory.

Design choices worth recording:

- **It is a builtin, not a plain argv-builder, on purpose.** screencapture often
  exits 0 even when the Screen Recording permission is denied — it just writes an
  empty/desktop-only file. Detecting that requires running the tool AND THEN
  inspecting the output file, which only an in-process builtin can do (an
  argv-builder never sees the result). `screencapturePermissionError` turns a
  non-zero exit / missing / zero-byte file into an actionable "grant Screen
  Recording in System Settings → Privacy & Security" hint, mirroring
  `messagesDBError`/`appScriptError`.

- **Default location is `~/Pictures/Screenshots`; the caller may override it.**
  An early version saved to `/tmp/mcp-fallback/screenshots`, but `/tmp` on macOS is
  cleaned by `/usr/libexec/tmp_cleaner` (a launchd job at midnight) and isn't where
  a user looks for a screenshot, so the default moved to a durable, discoverable
  `~/Pictures/Screenshots` (created on first use). The optional `output_path` param
  lets the user pick a destination in the same prompt: an existing directory gets a
  generated filename inside it; anything else is treated as a full file path (parent
  created if missing), with a recognized image extension on the name overriding the
  `format` param. Captures are **create-only** — `output_path` refuses to overwrite
  an existing file — which keeps the operation safely in the read-only lane (no
  destructive side effect, so no stage→execute gate). The path is still guarded:
  `resolveScreenshotPath` rejects a leading `-` (the same option-injection guard
  `mkdir`/`open_file` use, since the path is screencapture's trailing operand) and
  an unsupported extension; `display` is a typed int (validated `>= 1`) and `format`
  an allowlisted enum. Generated filenames carry a nanosecond timestamp so rapid
  successive captures never collide.

- **Classified `read_only` / `low` risk despite writing a file.** The only side
  effect is creating a fresh artifact in a server-owned scratch directory the
  caller explicitly asked us to fill; it never touches user-managed state and
  needs no undo, so the read-only lane (immediate, no stage→execute round-trip)
  is the right fit for an "eyes on the screen" agent loop. Risk is `low` (not
  `none`) because the image can expose whatever is currently on screen.

- **Dimensions only for PNG/JPEG.** `imageDimensions` uses `image.DecodeConfig`
  with the stdlib PNG and JPEG decoders blank-imported; PDF/TIFF have no stdlib
  decoder, so their dimensions are simply omitted from the summary rather than
  failing the capture.

**issue**

Region and window capture are not implemented — `capture_screen` only photographs
a whole display. Window capture by app name has no clean non-interactive path:
`screencapture -l <id>` needs a CoreGraphics window ID, and there is no stable
CLI to enumerate those without cgo (`CGWindowList…`) or a third-party tool, while
the interactive selection modes (`-i`/`-s`/`-w`) are banned in an agent loop.
Region capture (`-R x,y,w,h`) is feasible and a natural follow-up; window capture
is deferred pending a window-ID resolution strategy.

A second, smaller limitation: permission detection catches only *hard* failures
(non-zero exit, missing or zero-byte file). On some macOS versions a denied Screen
Recording grant still yields a non-empty desktop-only image, which cannot be
reliably distinguished from a real capture after the fact. A definitive check would
need the `CGPreflightScreenCaptureAccess()` API via cgo, which is out of scope for
the current pure-Go build.

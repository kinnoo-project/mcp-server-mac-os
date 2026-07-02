**note**
Design choices for the `clipboard` tool (U5 of the capability roadmap): a new
domain exposing two operations — `read_clipboard` (read-only) and
`write_clipboard` (reversible, auto-commit).

- **A new tool/category, not an extension of an existing domain.** The clipboard
  is its own everyday surface ("what did I just copy?", "copy this to my
  clipboard"), so it earns its own MCP tool. Because the server projects one
  domain tool per capability `category`, adding `clipboard.json` with
  `"category": "clipboard"` is all it takes to surface a new tool — no server
  code changes. The integration tool-surface count moved 18 → 19.

- **`read_clipboard` is a builtin over `pbpaste`, purely to bound its own
  output.** The clipboard is a property of the user's window session (not this
  process), so — unlike `pwd` — the answer must come from the system pasteboard
  via the trusted `/usr/bin/pbpaste`. It is a builtin (rather than a
  generic-builder capability) for one reason: a builtin's output bypasses the
  subprocess truncation budget, and a paste can be huge, so the builtin runs its
  result through the same head/tail `compactOutput` the subprocess layer uses.
  Argv is fixed (`pbpaste -Prefer txt`), so there is no injection surface and no
  `reviewedFreeTextBuiltins` entry is needed. Empty output means the clipboard is
  empty OR holds non-text content (an image/file); both are reported as a clear
  note rather than a blank result.

- **`write_clipboard` is a mutator whose text travels on stdin, so there is no
  argv surface at all.** Forward is `pbcopy` with the new text on stdin — a pure
  data sink: stdin bytes become the clipboard verbatim and can never be parsed as
  flags or code (`Command.Stdin`, U0). `pbcopy` takes no operand, so the
  model-controlled text has no argv path to begin with; the injection regression
  (`TestPlanWriteClipboard_HostileTextLandsAsData`) pins that a battery of
  flag-like/shell-active values land verbatim on stdin with an empty argv.

- **Undo restores the prior clipboard text — with two honest "no undo" cases.**
  At stage time the mutator probes the current clipboard with a read-only
  `pbpaste` and bakes those bytes into an inverse `pbcopy`, giving a byte-exact
  restore (mirroring `append_to_file`'s prior-contents inverse). Two cases yield
  `Inverse == nil`, stated plainly in the preview: (1) the prior clipboard was
  empty or non-text — `pbpaste` returns no text, so there is nothing to restore
  to, and clearing the clipboard would NOT bring an image back, so pretending to
  undo would be a lie; (2) the prior text exceeds the 1 MiB cap kept for undo.
  Both the new text and the prior payload are capped at 1 MiB (well under the
  engine-wide stdin backstop) since both sit in the token stores until consumed.
  The inverse sets `DiscardStdout` so undo never echoes the prior (possibly
  secret) value back — `pbcopy` emits nothing anyway, but the intent is explicit.

- **Auto-commit lane (reversible / low risk).** Replacing the clipboard is a
  benign, low-stakes side-effect, so it runs immediately (like `open_application`)
  rather than waiting behind the `execute` token gate — but it still goes through
  `Stage` first so the inverse is computed, and returns an `undo_token` when the
  prior contents were restorable.

- **Testability: pure decision halves.** `read_clipboard`'s rendering
  (`formatClipboardText`) and `write_clipboard`'s plan assembly
  (`planWriteClipboard`, fed the prior bytes directly) are split out from their
  subprocess wrappers so every branch is unit-testable WITHOUT touching the live
  clipboard — a live `pbpaste` read in a test could surface a user's actual
  copied secret. The write+undo round trip against the real pasteboard is a
  manual eval (`m_clipboard_write_then_undo`) because it clobbers the live
  clipboard mid-run; `read_clipboard` selection is an automated (A) case.

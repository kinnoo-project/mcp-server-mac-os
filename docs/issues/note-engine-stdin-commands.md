**note**
The mutation seam's `engine.Command` now carries an optional `Stdin []byte`
payload, and `Engine.RunCommand` feeds it to the child's standard input via the
same `execCommand` primitive the pipeline already used. Design points:

- **Why stdin instead of argv:** bytes on stdin can never be parsed as flags,
  paths, or script by the target binary, so content payloads (file bodies,
  clipboard text) need no dash-guard or `--` hardening — the channel itself is
  the injection defense. This is the prerequisite for `write_file` /
  `append_to_file` (`tee`) and `write_clipboard` (`pbcopy`), whose content is
  model-controlled.
- **Stage→execute→undo transparency:** staged plans and undo payloads are
  stored as `Command` values in the token stores, so a payload captured at
  stage time — including prior file/clipboard contents baked into an inverse —
  replays through commit and undo byte-for-byte with zero server changes.
- **`previewSnippet`:** a shared helper renders content payloads in previews as
  a byte count plus a quoted, truncated first line, so a human sees what will
  be written without a huge or control-character-laden dump in the
  confirmation text (`%q`-style escaping guards terminal mangling).
- Regression tests: `TestRunCommand_StdinIsPureData` (hostile flag-like/
  metacharacter content round-trips verbatim through `cat`),
  `TestRunCommand_NilStdinUnchanged`, `TestPreviewSnippet`.

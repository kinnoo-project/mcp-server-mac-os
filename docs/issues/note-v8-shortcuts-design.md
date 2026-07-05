**note**

V8 — new `shortcuts` domain: `list_shortcuts` (read-only) and `run_shortcut`
(the project's highest-risk operation). This un-tables the deferred design in
`docs/ideas/shortcuts-runner-deferred.md`, implemented exactly as specified.
The domain adds the 22nd MCP tool (`shortcuts`) — 25 tools total with
execute/undo/pipeline.

## The two operations

- `list_shortcuts` — RO/none builtin over `shortcuts list --show-identifiers`.
  Fixed argv, no model input, no injection surface. An empty library or an
  unavailable Shortcuts service is surfaced as a friendly message (data), not an
  error.
- `run_shortcut` — **ST / high / irreversible** mutator over
  `shortcuts run -- <name>` (optional `-i <input-file>`).

## Why run_shortcut is the highest risk tier

A shortcut is arbitrary automation authored *outside* this server: it can send
messages, toggle Focus/HomeKit, delete files, hit the network — anything the
Shortcuts app can do. Its blast radius is unbounded and there is no meaningful
inverse (you cannot "un-run" a shortcut's side effects). So it is classified
**high risk / irreversible**, always staged behind the human-approval gate, never
auto-committed, and pinned into `dangerousOps` in
`internal/registry/security_invariants_test.go` so that classification can never
be softened by a later manifest edit.

## Injection posture (three layers)

`shortcuts` is a Swift ArgumentParser tool, so it honours a `--` end-of-options
terminator (verified on-device: `shortcuts run --help` shows the positional name
+ `-i/--input-path`). The one model-controlled name is defended three ways:

1. **`--` terminator.** The forward argv is `run [-i <path>] -- <name>`; the name
   always rides after `--`, so even a dash-leading name (`-e`, `--output-path`)
   lands as the positional shortcut name, never as one of `shortcuts`' own flags.
   Pinned by `TestShortcutsRun_TerminatorPlacesNameLast`.
2. **Up-front `validateShortcutName`.** Belt-and-braces: empty, dash-leading, and
   control-laden names are rejected before argv is built (consistent with the
   other mutators). Spaces are allowed — users name shortcuts freely.
3. **Stage-time existence check.** Staging runs `shortcuts list` and refuses to
   stage a run whose name is not in the returned set, so a human is never asked to
   confirm a no-op or a near-miss name, and the preview can only ever name a real
   shortcut.

The optional `input` file goes through `validateExistingOperand` (dash-rejected,
resolved absolute, must exist) before becoming an `-i <path>` flag value.

## Why the `shortcuts` binary is not verb-pinned like V5/V7 binaries

V5 (`csrutil`/`spctl`) and V7 (`tmutil`/`diskutil`/`hdiutil`) came *off* the
registry deny list and moved their safety to `security_verbs_test.go` (a closed
set of allowed sub-verbs). `shortcuts` was never on the deny list, and its danger
is not a destructive *sub-verb* — the dangerous act is running *any* shortcut,
which is precisely the capability we expose. Verb pinning would add nothing here;
the safety story is the staging gate + the `dangerousOps` pin instead. Listing is
kept structurally separate (fixed `list` argv) so it can never trigger a run.

## No auto-undo (consistent with the irreversible-op precedent)

`run_shortcut` carries `Inverse: nil` — a shortcut's side effects cannot be
reversed. The preview states plainly that the run "CANNOT be undone", matching how
`send_mail`/`call`/`terminate_process` present themselves.

## Manual smoke test (not run in CI)

The live run path is not exercised by the unit tests (they stop at staging /
validation). To verify end-to-end: create a benign shortcut named
`MCP Eval Ping` (e.g. a single "Show Notification" action), then drive the
`m_run_benign_shortcut` eval case (`evals/cases/shortcuts.json`) — it stages the
run (naming the real shortcut) and the confirmation executes it. Confirm the
shortcut's action actually fired. `run_shortcut` requires no special TCC grant
beyond the user's own permissions, but the shortcut it runs may itself trigger
permission prompts (Automation, HomeKit, etc.) the first time.

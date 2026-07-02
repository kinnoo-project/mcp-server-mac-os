**note**

Unit 8 of the capability-expansion roadmap (`~/.claude/plans/woolly-noodling-stream.md`)
adds two capabilities to the existing `preferences` domain (no new MCP tool):
`read_setting` (read back a curated preference's current value) and
`set_appearance` (switch the system-wide Dark/Light mode).

Design choices worth recording:

- **read_setting shares write_setting's allowlist, not just its enum.** It takes
  the SAME closed `setting` enum and resolves it through the SAME
  `defaultsAllowlist` map (mutate_preferences.go), so the real domain/key pair
  behind each setting lives in reviewed Go code, never in model input — the same
  data-not-code posture write_setting takes. A consequence: a setting is either
  both readable and writable through the curated list, or neither. A new
  cross-package drift guard (`TestReadSettingEnum_MatchesDefaultsAllowlist`,
  internal/server) keeps read_setting's manifest enum in lockstep with the
  allowlist, mirroring the existing write_setting guard. Because `setting` is an
  enum (not free text), read_setting needs no `reviewedFreeTextBuiltins` entry.

- **read_setting renders on/off/unset.** Every curated setting is a boolean
  toggle, so the value is normally "1"/"0" or unset; those render as
  on/off/"unset (macOS using its built-in default)". A value of any other shape
  (a setting hand-edited to a non-boolean) is reported verbatim rather than
  guessed at — a read should surface reality, not sanitize it.

- **set_appearance is the first System Events MUTATION, and the first System
  Events Automation TCC target.** Switching Dark/Light has no first-party command
  line (`defaults write -g AppleInterfaceStyle` does not reliably repaint the
  running session), so it drives System Events' `appearance preferences` through
  the shared `osascriptCommand` seam. The desired state ("true"/"false") arrives
  as data after the `--` terminator; the script body is a fixed constant and the
  model only ever picks the closed "dark"/"light" enum, which the mutator maps to
  the boolean token.

- **The prior state is probed via System Events, not `defaults read -g`.** The
  approved plan suggested probing current appearance with
  `defaults read -g AppleInterfaceStyle` (no TCC) to build the inverse. This
  implementation instead probes via System Events (`get dark mode`), for two
  reasons: (1) the forward NEEDS the System Events Automation grant anyway, and
  since set_appearance is auto_commit the forward fires immediately after
  staging — so probing via defaults would only let staging "succeed" moments
  before the forward failed on the missing grant. Probing via System Events
  surfaces a missing grant as ONE clear, mapped error up front. (2) It matches
  every other mutator in the codebase (Calendar/Reminders/Photos all probe prior
  state through osascript and map the error). The mapping is the new shared
  `systemEventsScriptError` helper (mutate_preferences.go), which
  window-management (Unit 11) will reuse for its own System Events reads.

- **set_appearance is reversible/low and auto_commit.** Flipping appearance is a
  benign, instantly-reversible cosmetic change, so forcing a stage→execute
  round-trip would be pure friction — it runs in the auto-commit lane
  (registry-enforced: auto_commit only legal at risk none/low) but still returns
  an undo token whose inverse restores the appearance observed at stage time.
  Unlike notify/speak, it is NOT irreversible: the inverse is a real System
  Events command, so the auto-commit path returns a usable `undo_` token rather
  than the "cannot be undone" suffix.

- **The pure plan-builder is split out for testing.** `planSetAppearance(wantDark,
  priorDark)` assembles the forward/inverse/preview with no side effects, so the
  argv layout, `--` terminator, and preview text are unit-tested without a live
  (permission-gated) System Events probe (mutate_appearance_test.go). This mirrors
  the formatClipboardText/planWriteClipboard split in the clipboard unit.

- **Eval classification.** read_setting gets CI-safe selection cases
  (`read_dock_autohide`, `read_finder_hidden_files` in
  `evals/cases/preferences_reads.json`) — `defaults read` succeeds whether the
  key is on, off, or unset. set_appearance is manual (`m_set_appearance_then_undo`
  in manual_smoke.json): it is auto_commit, so invoking it flips the real screen
  appearance and needs the Automation grant — there is no CI-safe selection-only
  turn, matching how every other side-effecting auto_commit op is verified.

- **Live smoke.** read_setting was smoke-tested through a freshly built binary
  over stdio (`read_setting dock_autohide` → "Dock: auto-hide: on (true).",
  matching the machine's actual state). The installed MCP client binary is stale
  (predates this unit), so in-session `/runevals` cannot yet route to the new ops
  — routing was validated via `runevals -dry-run`; the stdio call is the live
  read check. set_appearance's live toggle+undo is on the manual checklist.

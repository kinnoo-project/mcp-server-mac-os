**note**
Design choices for the Settings deep-links & guided hand-offs unit (U3 of the
capability roadmap): three new `open_settings` panes — `focus`, `keyboard`,
`apple_id` — plus click-path guidance in the tool description.

- **Hand-off, not automation.** Pairing a Bluetooth device, signing into an
  Apple Account/iCloud, toggling a Focus/Do Not Disturb, adding a keyboard
  language, and starting screen mirroring have NO first-party command line on
  macOS (admin or not). Automating them via UI scripting would be
  layout-fragile and Accessibility-gated. The durable answer is to open the
  exact System Settings pane and tell the user precisely what to click — so
  the tool description now instructs the model to ALWAYS include the
  click-path in its reply (e.g. bluetooth → "put the device in pairing mode,
  then click Connect next to it under Nearby Devices").
- **No new op, no new surface.** `open_settings` already existed
  (auto-commit, irreversible/low — it just opens a window). The model still
  never supplies a URL: it picks a pane from a closed enum and the engine
  maps it to a vetted `x-apple.systempreferences:` identifier Go-side, so
  there is no injection surface and no TCC grant involved.
- **The `apple_id` identifier is deliberately odd.** Ventura+ panes are
  mostly `com.apple.<Name>-Settings.extension`, but the Apple Account pane
  kept the legacy `com.apple.systempreferences.AppleIDSettings` identifier.
  `TestStageOpenSettings_HandoffPaneURLs` pins all three new URLs exactly,
  because the pre-existing tests only checked that each pane resolved to a
  *non-empty* URL — a typo would have passed them and silently opened
  System Settings at its default pane (the `open` command's graceful
  degradation for unknown identifiers).
- **Drift guards already in place.** `TestSettingsPanes_MatchManifestEnum`
  (server) keeps the manifest `pane` enum and the engine's map 1:1, and
  `TestStageOpenSettings_EveryPaneHasURL` (engine) re-checks completeness —
  both auto-extended to the new panes with no changes.
- **Evals are routing + guidance assertions.** Three cases in
  `system_reads.json` ("pair my wireless mouse", "sign into iCloud", "turn on
  Do Not Disturb") assert the model selects `open_settings` (never `execute`)
  and that its reply contains the guidance keyword ("pairing" / "iCloud" /
  "Focus"). The eval harness has no per-parameter assertion, so the pane
  choice is checked indirectly through the guidance text. Like the
  pre-existing `open_display_settings` case, these DO open a real Settings
  window when run — benign, self-dismissing residue.

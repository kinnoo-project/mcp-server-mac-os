**issue**

Several system-control actions a "command center" would ideally perform directly
require administrator rights that cannot be obtained over this server's
non-interactive stdio transport (`sudo` would need an interactive password prompt
or NOPASSWD, and is a hard line per the project rules). They are therefore
**deferred**, and the server provides a guided fallback instead of failing:

- **Connecting / re-enabling a printer** — `lpadmin` (add/configure) and
  `cupsenable`/`cupsaccept` (re-enable a disabled queue) require admin. The
  `printer` domain ships list/status/print only; `list_printers` flags a disabled
  queue and points the user at `system.open_settings` (pane `printers`).
- **Low Power Mode toggle** — `pmset -a lowpowermode 1` requires admin. `system`
  exposes `power_status` (read) only; the toggle is left to Settings.
- **Bluetooth / Wi-Fi power toggles** — no first-party non-admin CLI exists
  (`blueutil` is third-party and would be rejected by the directory-based policy
  allowlist anyway). Reads (`bluetooth_status`, `wifi_status`) are provided; the
  toggles route through `open_settings` (panes `bluetooth` / `wifi`).
- **Listing nearby/available Wi-Fi networks to join** — the legacy `airport -s`
  scan CLI was removed in recent macOS, and `wdutil` requires sudo.
  `list_preferred_wifi` (remembered networks) is provided; joining a new network
  routes through `open_settings` (pane `wifi`).

The bridge for all of these is the `system` domain's `open_settings` operation,
which deep-links the user to the exact System Settings pane to finish with a
click. Its pane→URL map uses the **macOS 13 Ventura+** `x-apple.systempreferences:`
identifiers (the project's minimum supported OS), so no per-version handling is
needed; an unrecognised identifier still opens System Settings to its default
pane, degrading gracefully.

**fixed**

Not a defect — these are intentional scope boundaries, mitigated by the
`open_settings` guided fallback. Revisit only if a supported non-admin path
appears (or if the project ever opts into a privileged helper).

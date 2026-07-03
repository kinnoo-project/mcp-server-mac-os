**note**

U15 — `system` domain gains two hardware-power controls: `display_sleep` (turn
the screen off now) and `wifi_set_power` (turn the Wi-Fi radio on/off). Both
extend the existing `system` tool; no new MCP tool. Design decisions worth
recording:

## The `wifi_set_power` admin contingency — resolved in favour of shipping

The build plan flagged `wifi_set_power` as *contingent*: the standing issue doc
(`issue-system-control-admin-gated-gaps.md`) claimed Wi-Fi power toggling had "no
first-party non-admin CLI", so step one of implementation was a **live non-admin
check** of `networksetup -setairportpower`. That check ran a no-op set of the
radio to its *current* power state and it completed with exit 0 and **no password
prompt** — proving `-setairportpower` is not admin-gated on macOS 13+. So the op
ships as a real toggle rather than degrading to the `open_settings` (pane `wifi`)
hand-off. The issue doc was amended to record this.

The narrower gap that remains real: *joining a specific network* still has no
non-admin CLI (the `airport -s` scan was removed from recent macOS and
`wdutil`/network-add need sudo), so picking a network continues to route through
`open_settings`. `wifi_set_power` is only the radio on/off switch.

## Why the two ops sit in different lanes

- **`display_sleep` → irreversible / low / auto-commit.** Putting the display to
  sleep is instant and benign, and the *only* way back is a human act (press a
  key, move the mouse) — there is no command to "undo" it. That matches the
  `open_settings`/`notify`/`speak` precedent: a harmless, immediately-reversible-
  by-a-human action does not earn a stage→execute round trip. It uses `pmset
  displaysleepnow`, which (unlike `pmset -a <setting>`) needs no admin and does
  not sleep the whole machine or lock it — only the screen turns off.

- **`wifi_set_power` → reversible / medium / STAGED (never auto-commit).**
  Turning Wi-Fi off severs the machine's connectivity — exactly the consequential,
  easy-to-regret change the confirmation gate exists for. Registry validation
  already forbids auto-commit at medium risk, so the staging is structurally
  enforced, not just conventional. Staging probes the radio's current power with
  the read-only `networksetup -getairportpower` and bakes a restore-to-that-state
  inverse, so undo returns Wi-Fi to whatever it was before — including the no-op
  case where the requested state already matched.

## Injection surface: none for either op

Neither op takes a free-text parameter. `display_sleep` has zero params and a
fully fixed argv. `wifi_set_power`'s only param is a closed `on`/`off` enum
(validated by the registry and re-checked in the mutator), and the interface name
is resolved from macOS's own hardware-port listing (`parseWifiDevice`, reused from
`wifi_status`), never from model input. So no argv operand is attacker-controlled
free text and there is no dash-leading value to guard — hence no
`reviewedFreeTextBuiltins` entry (that gate is for free-text *builtins*, and these
are mutators with no free text anyway).

## Testing

Pure halves (`planWifiSetPower`, `parseWifiPower`) are split out so the argv
layout, the prior-state-baked inverse, and the power-line parse are unit-tested
without ever toggling the real radio or sleeping the real display. `display_sleep`
is asserted through its state-free mutator directly. Live behaviour is covered by
manual eval cases only (`m_display_sleep`, `m_wifi_set_power_stages_only`); the
Wi-Fi case is deliberately kept stage-only so even a by-hand run never severs the
eval runner's own connectivity.

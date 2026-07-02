**note**

Unit 7 of the capability-expansion roadmap (`~/.claude/plans/woolly-noodling-stream.md`)
adds two read-only lookups to the existing `system` domain (no new MCP tool):
`list_airplay_devices` (AirPlay receivers reachable on the local network) and
`list_input_sources` (enabled keyboard layouts / input methods, with the active
one marked). They pair with the guided System Settings hand-offs from Unit 3:
these reads answer "what's available / what's set", while *starting* mirroring
(`displays` pane) or *adding* a language (`keyboard` pane) has no first-party CLI
and is handed off via `open_settings`.

Design choices worth recording:

- **`list_airplay_devices` runs `dns-sd -B _airplay._tcp` under a bounded
  context deadline, and reads the output back FROM the timeout.** `dns-sd -B` is a
  *continuous* Bonjour browser — it never exits on its own, printing a line each
  time a service appears/disappears. There is no "stop after N results" flag, so
  the builtin wraps `runCommand` in a short `context.WithTimeout` (3s), exactly the
  bounded-run pattern used by `ping`/`scan_lan` in `builtins_network.go`. The
  deadline kills `dns-sd`, and `execCommand` deliberately returns the stdout
  captured up to that point ALONGSIDE the deadline error (see executor.go), so the
  "timed out" path is the EXPECTED, successful one here and the builtin parses
  `res.Stdout` from it. A genuine launch failure is told apart by the derived
  context NOT having hit its deadline (and the parent context not being cancelled).
  Do **not** substitute `system_profiler SPAirPortDataType` — that reports Wi-Fi
  radio state, not AirPlay receivers; `dns-sd` is the only source.

- **Browse-row parsing keeps device names with spaces/quotes intact and honours
  Add/Rmv.** A row is `<timestamp> <Add|Rmv> <flags> <if> <domain> <service>
  <instance name…>`; the instance name is everything from the 7th column on (e.g.
  `55" The Frame`, `Jerry's MacBook Air`) and the same receiver is advertised once
  per network interface, so names are de-duplicated. Add sets a device present,
  Rmv clears it, so a receiver that announced then withdrew inside the window is
  not reported. The empty result is normal (nothing powered on, or the one-time
  macOS **Local Network** privacy prompt not yet granted) and is explained in the
  output rather than treated as an error.

- **`list_input_sources` reads two HIToolbox preference keys and parses the
  old-style plist by hand.** `defaults read com.apple.HIToolbox
  AppleEnabledInputSources` returns the enabled sources and
  `AppleSelectedInputSources` the active one(s). `defaults` prints these as an
  "old-style" (NeXTSTEP) property list, not JSON, and there is no `-json` form for
  a single key; the Go standard library has no plist parser and the project avoids
  new dependencies, so `parsePlistDicts` is a small purpose-built reader for
  exactly this shape (a flat array of `{ key = value; }` dicts). The selected read
  is best-effort: if it fails we still list the enabled sources, just without a
  "current" marker, because listing them is the primary answer.

- **Only user-facing typing sources are shown.** The enabled list also contains
  helper input methods (character palette, press-and-hold, emoji) that carry no
  selectable layout; these are filtered out as noise. Keyboard Layout entries show
  their name (e.g. "U.S."); Input Mode entries show the last dot-component of their
  reverse-DNS id (e.g. `…SCIM.ITABC` → "ITABC") plus the full id as detail.

- **No injection surface.** Neither op takes a model-controlled parameter — both
  assemble fixed argv from constants — so there is no `reviewedFreeTextBuiltins`
  entry and no `-e`/dash-leading regression to ship (the injection sweep only
  covers free-text builtins). Both binaries (`dns-sd`, `defaults`) resolve under a
  trusted system directory and are not on the deny list. No TCC/Automation grant is
  needed, though the first AirPlay browse may raise the Local Network prompt.

- **Verification.** The pure parsers/renderers are unit-tested against captured
  real output (`builtins_devices_test.go`). Both ops were additionally smoke-tested
  end-to-end against live system state through the real registry+engine (input
  sources correctly marked U.S. active and filtered the helper methods; the AirPlay
  browse found two receivers within the 3s window and de-duplicated them across
  interfaces). `list_input_sources` is a CI-safe (A) eval since every Mac has ≥1
  source; `list_airplay_devices` is Manual (hardware/network dependent, ~3s browse).

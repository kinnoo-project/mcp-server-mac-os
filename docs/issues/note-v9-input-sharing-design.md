**note**

# V9 — system: input remapping (hidutil) + sharing status

Design decisions and deviations for the V9 unit (`feat/system-input-sharing`),
which adds three `system` operations — `remap_key`, `key_remap_status`,
`sharing_status` — plus a `sharing` pane on `open_settings`. No new domain and no
deny-list edit (hidutil was never denied), so the tool count stays 25.

## `sharing_status` uses a loopback TCP probe, not lsof/launchctl

The plan sketched `sharing_status` as an lsof-listener check plus `launchctl
print` for the service labels. On-device testing (before writing any code) showed
**both are unreliable without root**:

- A non-root `lsof -i` on macOS only reports the CURRENT user's own sockets. The
  sharing daemons (`sshd`, `screensharingd`, `smbd`) all run as **root**, so
  lsof-as-user reports them "off" even when they are on. Verified: on this Mac
  `lsof -nP -iTCP -sTCP:LISTEN` as a normal user listed only `jerry`-owned
  processes.
- `launchctl print system/<label>` is root-gated (returns "Bad request." /
  permission errors as a normal user), and `launchctl list` shows only the
  per-user agents, not the system daemons.

What DOES work unprivileged is asking the kernel to connect: enabling any of these
services makes its daemon listen on a well-known TCP port on **all** interfaces,
including loopback. So `sharing_status` attempts a short, immediately-closed TCP
connection to `127.0.0.1` on 22 (Remote Login/SSH), 5900 (Screen Sharing), and
445 (File Sharing). A successful connect ⇒ listening ⇒ on; a refusal ⇒ off. It is
a pure in-process check (`net.Dialer.DialContext`), so there is **no subprocess,
no input, and no injection surface** — which also means it needs no
`reviewedFreeTextBuiltins` entry.

Documented limitation: a service deliberately reconfigured to bind only a
non-loopback interface would read as "off". The standard macOS toggles always bind
loopback too, so in practice the signal is exact. `systemsetup -getremotelogin`
(the "authoritative" answer for SSH) was **dropped** for the same root-gating
reason the plan anticipated — it prompts for admin rights and cannot run over this
server's non-interactive transport.

## `remap_key` — curated enum, JSON built (never interpolated), probe-based undo

`hidutil property --set` can remap any key to any other; an arbitrary raw mapping
is unreviewable and can brick a keyboard. So `remap_key` takes a **closed enum**
(`caps_lock_to_escape`, `caps_lock_to_control`, `swap_command_option`,
`swap_control_command`, `disable_caps_lock`) that selects a row in a Go table of
vetted HID-usage pairs — the mapping never comes from model input. A
cross-package test (`TestRemapEnumMatchesCuratedTable`) pins the enum and the Go
table together, mirroring the `open_settings` pane guard.

Both the forward mapping (curated pairs) and the inverse mapping (parsed prior
state) are assembled with `encoding/json` from typed integers via a single shared
builder, so the payload is always valid JSON with no room for an injected
fragment. `disable_caps_lock` maps Caps Lock to HID keyboard usage `0x00` ("no
event indicated") — macOS's standard way to disable a key.

Undo needs a state probe (like `write_setting`, unlike `mkdir`): staging first
reads the current `UserKeyMapping` via `hidutil property --get`, converts that
old-style-plist output (decimal values, `Dst` before `Src`) back into settable
JSON, and bakes it into the inverse — so undo restores exactly what was there
before, whether that was empty, a curated remap, or the user's own custom mapping.
The empty state marshals to `{"UserKeyMapping":[]}` (the clearing document), never
`null`. Curated-match detection for `key_remap_status` is order-insensitive because
hidutil does not preserve the order the pairs were set in.

Reboot caveat: hidutil remaps do **not** survive a restart (macOS clears
`UserKeyMapping` on boot). The tool summaries and the stage preview say so;
persistence would require a login item, which is out of scope.

## hidutil verb pinning

hidutil was not on the deny list, so no `security_verbs_test.go` edit was required.
Its two-verb surface (`property --set` for the one mutation, `property --get` for
the read) is pinned instead by `hidutilSetArgs`/`hidutilGetArgs` and asserted in
`mutate_system_input_test.go` (`TestRemapKeyArgvPinned`), so a future edit cannot
grow an unreviewed hidutil verb.

## Manual smoke tests still owed

- Apply a real `remap_key` (Caps Lock → Escape), confirm the key behaves, then
  undo and confirm it reverts — the `m_remap_key_roundtrip` eval case
  (`system_input_sharing.json`).
- With Screen Sharing / Remote Login actually enabled, confirm `sharing_status`
  flips the corresponding line to ON (the CI test only proves the probe mechanism
  against a self-bound port, since the host's real services may be off).
- Confirm `open_settings` pane `sharing` opens the Sharing pane (identifier
  `com.apple.Sharing-Settings.extension`, verified present in
  `/System/Library/ExtensionKit/Extensions/Sharing.appex`).

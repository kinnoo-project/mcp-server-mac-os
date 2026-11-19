# macOS MCP server: daily ideas, 2026-11-18

Today's focus: UX and reliability (first-run permissions, "what can I do here?"), plus a landscape check for what other Apple-app MCP servers do about setup.

Method note: the shell was denied again, so there was no `man`, `sdef` or `git log`. I read the `main` manifests and issue notes with Grep/Read and ran two web searches. Anything needing a local man page or dictionary is marked *unverified*.

## TL;DR

1. **Ship the "small three" (output volume, `speed_test`, iCloud status). Revisited, seventh day running.** The `system.json` operation list on `main` still has no volume op, and no manifest calls `networkQuality` or `brctl`. Either build them or record a reason in `docs/issues/`.
2. **Add a read-only `doctor` / permission preflight (new).** Many operations need a one-time macOS permission (Automation, Full Disk Access, Screen Recording). When one is missing the user just sees a failure. A single read-only check that says "Mail: allowed, Messages history: needs Full Disk Access, here is the pane" would make the server far easier to adopt.
3. **Finish the Finder follow-up set: `reveal_in_finder`, `checksum`, Finder tags (read).** Still absent. They are small and need no admin.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Permission preflight (new) | "Why can't you read my messages?" | `permissions_check` (system) | In-process builtin that runs harmless probes per app and maps the failure to a Settings pane | Read | Hand-off to the Privacy & Security pane | M | **P1** |
| Output volume (**Revisited**) | "Turn the volume down" | `get_volume`, `set_volume` (system) | Fixed `osascript` script, integer passed as argv after `--` | Staged; undo restores old level | No | S | **P1** |
| Speed test (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality` (*unverified* on Ventura; it ships from macOS 12) | Read, about 20s | No | S | **P1** |
| Reveal in Finder (**Revisited**) | "Show me that file" | `reveal_in_finder` (filesystem) | `open -R <path>` (*unverified*) | Changes no files | No | S | P1 |
| Checksum (**Revisited**) | "Does this download match the hash?" | `checksum` (filesystem) | `/usr/bin/shasum`, closed algorithm list | Read | No | S | P2 |
| Safari actions (**Revisited**) | "Open this in Safari / close that tab" | `open_url_in_safari`, `close_tab` (safari) | Named AppleScript builder | Staged; close is not undoable | No (Automation) | M | P2 |
| iCloud status (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `brctl` (*unverified*) | Read | No | M | P3 |

## New ideas

1. **`permissions_check` (the main new idea).** What it is: one read-only operation that tries a cheap, harmless probe for each permission the server depends on and reports allowed, denied, or not yet asked. Value: today the model cannot tell "this op is broken" from "you have not granted access yet", and the user gets a raw error. How: an in-process builtin that reuses existing read ops as probes (for example a count query against Notes, a one-row read against Messages history), and returns the matching System Settings pane for each failure through the existing `open_settings` map. Safety: reads only, no user data in the output (counts and status only). It would reuse the existing handling of the `-600` and authorization-denied errors already documented in the project notes. Sources: airmcp's built-in doctor command (see Landscape).

2. **Capability discovery by app, not just by domain.** The model sees 26 tools with long menus. A tiny read-only `describe_domain` op that returns one domain's operations with parameters would let the model look up the exact shape instead of relying on a long tool description. This also helps with the menu-truncation concerns already recorded in `docs/issues/note-manifest-menu-compaction.md`. Pure registry read, no subprocess. Effort S.

3. **`shortcuts` depth: view and a named-Shortcut allowlist.** The `shortcuts` manifest has `list_shortcuts` and `run_shortcut`. A third-party guide describes `shortcuts view` and `shortcuts sign` subcommands (see Sources; *unverified*, confirm against `man shortcuts`). `view` opens a Shortcut in the editor, which fits the "hand the user the app" rule. Pair this with the already proposed `set_focus` convention: run only Shortcuts whose names are on a user-approved list, so arbitrary Shortcuts cannot be launched.

4. **Spotlight "Actions" awareness (Tahoe).** Tahoe lets users trigger Shortcuts from Spotlight ([MacMost](https://macmost.com/how-to-use-spotlight-actions-in-macos-tahoe.html)). This is not available on Ventura, so only document it; do not build against it.

5. **Post-failure guidance in the output.** When a read fails with a permission error, the engine could append a one-line hint naming the permission and pane. This is a cheap precursor to idea 1 and needs no new binary. Effort S.

## Landscape notes

- **airmcp** ([GitHub](https://github.com/heznpc/airmcp)): covers many Apple apps, lets users pick which apps to connect, and ships a doctor command that checks Node, config, client setup and macOS permissions. Borrow the doctor idea (as `permissions_check`) and the "connect only the apps you want" idea (an optional domain allowlist at startup). The listing text I saw is a summary; activity and stars are *unverified*.
- **macos-mcp (krmj22)** ([GitHub](https://github.com/krmj22/macos-mcp)): reads Reminders and Calendar through a compiled Swift EventKit helper and Notes through JXA. Reject the compiled helper: it would run a non-system binary and break the allowlist model.
- **Apple Events MCP (FradSer)** ([GitHub](https://github.com/FradSer/mcp-server-apple-events)): Reminders and Calendar only, via EventKit. Same rejection reason.
- **Orchard for macOS** ([listing](https://mcpservers.org/servers/orchard-5km-tech-apple-mcp-server)): a menu-bar app, about 52 tools across 11 apps, with Notes behind a paid tier. Third-party listing, so counts are *unverified*. Nothing to borrow, but it shows the market values one-click permission setup.
- **Tahoe Shortcuts:** the same sources describe new Mac automation triggers and Apple Intelligence actions inside Shortcuts ([9to5Mac](https://9to5mac.com/2025/07/30/shortcuts-in-ios-26-everything-new-coming-to-your-iphone-more/)). These are Tahoe-only, so treat as optional and do not depend on them with Ventura as the floor.
- Search did not return any new CLI tools in Tahoe, so the local binary list is unlikely to have grown. Not verified locally today.

## Bugs or reliability risks

None new that I could confirm. Standing item: `docs/issues/issue-no-executor-walltime-timeout.md` should be resolved or each op given its own cap before `speed_test` ships. I did not diff the manifests against the README counts today (174 operations, 23 domains); `docs/issues/note-doc-counts-drift.md` shows this drifts, so a count check in CI would be worthwhile.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (11-12 to 11-17):** still absent on `main`. Priority unchanged, except iCloud drops to P3 because `brctl` is unverified.
- **`reveal_in_finder`, `checksum`, Finder tags, `set_focus` (11-16/17):** still absent; unchanged.
- **Safari/Mail/Notes/Music depth (11-15):** still valid. Safari has only `list_tabs` and `current_tab`; Mail has no reply or mark-read; Notes has no move or delete; Music has no search or play-by-name.

## Sources

- [airmcp](https://github.com/heznpc/airmcp)
- [macos-mcp (krmj22)](https://github.com/krmj22/macos-mcp)
- [mcp-server-apple-events](https://github.com/FradSer/mcp-server-apple-events)
- [Orchard for macOS listing](https://mcpservers.org/servers/orchard-5km-tech-apple-mcp-server)
- [Shortcuts CLI guide (third party)](https://blakecrosley.com/guides/shortcuts)
- [9to5Mac: Shortcuts in iOS 26](https://9to5mac.com/2025/07/30/shortcuts-in-ios-26-everything-new-coming-to-your-iphone-more/)
- [MacMost: Spotlight Actions](https://macmost.com/how-to-use-spotlight-actions-in-macos-tahoe.html)
- Local: `main` manifests (`system.json`, `network.json`, `filesystem.json`, `safari.json`, `shortcuts.json`), `docs/issues/`.

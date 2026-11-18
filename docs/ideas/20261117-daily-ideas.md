# macOS MCP server: daily ideas, 2026-11-17

Today's focus: user needs (what people try to automate on a Mac) checked against the current `main` manifests, plus a coverage re-check of items already proposed.

Method note: the shell was denied again, so there was no `man`, `sdef` or `git log`. I read the `main` manifests, README and `docs/issues/` with Grep/Read and ran one web search. Anything needing a man page is marked *unverified*. `main` currently documents 23 domains and 174 operations (README Roadmap).

## TL;DR

1. **Ship the "small three": output volume, `speed_test`, iCloud status. Revisited, sixth day running.** A search of all 23 manifests shows no volume operation and no `networkQuality` or `brctl` call. These are the cheapest useful gaps and have been open since 11-12. If there is a reason for not building them, record it in `docs/issues/`.
2. **Add a Finder "after you find it" set: `reveal_in_finder`, `checksum`, Finder tags (read first).** Proposed 11-16 and still absent. The filesystem domain can find, copy, move and compress files, but cannot show one to the user or verify a download.
3. **Add a `set_focus` convention through Shortcuts, and a Spotlight-clipboard Settings hand-off.** Focus has no CLI, so use the existing Shortcuts path. Clipboard history in newer macOS is a Settings toggle, so hand the user off to the right pane instead of scripting it.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume (**Revisited**) | "Turn the volume down" | `get_volume`, `set_volume` (system) | Fixed `osascript` script; the integer goes in argv after `--` | Staged; undo restores old level | No | S | **P1** |
| Speed test (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality`, no flags | Read, ~20s | No | S | **P1** |
| Reveal in Finder (**Revisited**) | "Show me that file" | `reveal_in_finder` (filesystem) | `open -R <path>` (*unverified*; read `man open`) | No file changes; auto-commit | No | S | **P1** |
| Checksum (**Revisited**) | "Does this download match the hash?" | `checksum` (filesystem) | `/usr/bin/shasum`, closed algorithm enum | Read | No | S | P1 |
| Focus / DND (**Revisited**) | "Turn on Do Not Disturb" | `set_focus` (system) | Run a user-named Shortcut via the existing Shortcuts builder | Staged; undo runs the "off" Shortcut | No | S | P2 |
| Clipboard history setting (new) | "Turn on clipboard history" | Add `spotlight` to the `open_settings_pane` enum (system) | `x-apple.systempreferences:` Spotlight pane; exact pane ID *unverified* | Opens a pane only | Hand-off to Settings | S | P2 |
| Finder tags, read (**Revisited**) | "What's tagged Red?" | `get_tags` (filesystem) | `mdls -name kMDItemUserTags`, path guard | Read | No | S | P2 |
| iCloud status (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `brctl` (*unverified*) | Read | No | M | P2 |

## New ideas

1. **Spotlight / clipboard Settings hand-off.** Search results describe a built-in clipboard history in macOS Tahoe, turned on under System Settings, Spotlight (see Sources; this is from guides, not Apple, so *unverified*). The server cannot read that history and should not try. Adding a `spotlight` entry to the existing `open_settings_pane` enum lets the model say "I opened the pane, flip this switch". This is a one-line manifest change using the existing builder, so there is no injection surface. Confirm the pane ID on a real Tahoe Mac before merging. It must also degrade cleanly on Ventura, where the option does not exist.

2. **Backup freshness check.** The `storage` manifest already uses `tmutil`, so "when was my last backup?" may be answered already. Before building anything, check whether the existing op returns the latest backup date. If it only lists destinations, add a read-only `latest_backup` op. This is a check-first item, not a new build.

3. **Finder tags, read before write.** Reading uses Spotlight attributes (`mdls`), which has no `--` terminator I can verify, so reject dash-leading paths. Writing means editing a binary plist stored in an extended attribute, which is best done as an in-process Go builtin, staged, with undo restoring the old tag list. Ship read first.

4. **`set_focus` through Shortcuts.** Document that the user creates Shortcuts with fixed names (for example "Focus On" and "Focus Off"). The op runs only those names, from an allowlist, so arbitrary Shortcuts cannot be run. Needs Automation permission for Shortcuts. No new Go beyond the allowlist.

5. **`current_time` via `/bin/date`.** Models have no clock. A read-only op with a closed format enum and timezone allowlist costs almost nothing. P3.

6. **Property-list reader (`plutil -p`).** Read-only view of an app's Info.plist for "what version is this app?". Needs a path guard and a size cap. `plutil` options are *unverified*. P3.

## Landscape notes

No new landscape research today beyond the user-needs search. The standing conclusions from 11-15 and 11-16 hold:

- **Borrow:** Shortcuts-based Focus (cyanheads/macos-mcp-server), Finder tags and iCloud awareness (Apple-Tools-MCP, AirMCP), and the recipe-library idea from [macos-automator-mcp](https://github.com/steipete/macos-automator-mcp), with each recipe turned into a reviewed manifest entry.
- **Reject:** arbitrary script execution, runtime auto-generation of tools from scripting dictionaries, and GUI-automation fallbacks. These all bypass the stage, execute and undo model.
- Stars and release dates remain *unverified*, and I am not quoting any.
- The user-needs search returned only generic automation guides. It showed clipboard history, Finder tags, battery readouts and backup checks as common automation targets, but it is **not** evidence of what people ask AI assistants specifically.

## Bugs or reliability risks

None new. The standing dependency is `docs/issues/issue-no-executor-walltime-timeout.md`. It should be closed, or each op given its own time cap, before `speed_test` and any future long-running op ships.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (11-12 to 11-16):** still absent on `main`, confirmed by searching all 23 manifests. Priority unchanged.
- **`reveal_in_finder`, `checksum`, Finder tags, Focus (11-16):** still absent. Tags are now split so that the read half ships first (effort S), and Focus is unchanged.
- **Backup status:** newly flagged as a check-first item, because `tmutil` is already in `storage.json`.

## Sources

- [Take Control: Automating Your Mac (sample)](https://box.takecontrolbooks.com/samples/TCoAutomatingYourMac-6.0-sample.pdf)
- [Six Colors: Mac wishlist](https://sixcolors.com/post/2025/06/apple-keeps-checking-items-off-my-mac-wishlist/)
- [Raycast script commands, system gallery](https://www.mintlify.com/raycast/script-commands/gallery/system)
- [macos-automator-mcp](https://github.com/steipete/macos-automator-mcp)
- Earlier reports in `docs/ideas/` for the competitor links.
- Local: `main` manifests (`system.json`, `storage.json`, `network.json`, `filesystem.json`, `shortcuts.json`), README Roadmap.

# macOS MCP server: daily ideas, 2026-11-15

Today's focus: depth in first-party apps already covered (Safari, Mail, Notes, Music), plus a landscape refresh.

Method note: the shell was denied again, so I could not run `man` or `sdef`. Local checks were limited to listing and grepping the `main` checkout. Web search was available and used. Anything needing a man page is marked *unverified*.

## TL;DR

1. **Ship the "small three" in one PR: output volume, `speed_test`, iCloud status (Revisited, fourth day running).** I re-checked the manifests on `main` today. `system.json` still has no volume operation, and no manifest mentions `networkQuality`, `brctl`, `afconvert`, `afinfo`, `afplay` or `mdls`. Each is non-admin, and "turn it down" and "is my internet slow?" are among the most common requests.
2. **Give the four apps we already cover real actions, not just reads.** Safari has only `list_tabs` and `current_tab`. Mail has `search_mail`, `list_inbox`, `read_message` and `send_mail`, with no reply, mark-read or move. Notes can list, search, read, create and append, but not move or delete. Music has only now-playing, play/pause, next and previous. A user who gets "I can see it but can't touch it" stops asking.
3. **Add audio conversion and info (`afconvert`, `afinfo`).** Images and documents already convert, and audio and video do not. Copy the existing image-conversion design.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume and mute (**Revisited**) | "Turn the volume down to 20%" | `get_volume`, `set_volume` (system) | Fixed `osascript` script, integer 0–100 as argv after `--` | Read; change staged, undo restores old level | No | S | **P1** |
| Internet speed (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality`; no-argument run is enough, optional closed `-s` flag | Read-only, uses bandwidth ~20s | No | S | **P1** |
| Music: play something | "Play my workout playlist", "set Music volume" | `search_music`, `play_playlist`, `set_music_volume` (music) | Fixed AppleScript | Staged; undo restores previous state where possible | Automation permission | M | **P1** |
| Safari actions | "Open this in Safari", "close that tab", "add to Reading List" | `open_url_in_safari`, `close_tab`, `add_reading_list` (safari) | Fixed AppleScript | Staged; closing a tab is not undoable, so warn | Automation permission | M | P2 |
| Mail triage | "Reply to this", "mark all read", "archive it" | `reply_mail` (saves a draft, never sends), `mark_read`, `move_message` (mail) | Fixed AppleScript | Staged; mark-read undo flips the flag back | Automation permission | M | P2 |
| Notes move/delete | "Move this note to Work", "delete that note" | `move_note`, `delete_note` (notes) | Fixed AppleScript; delete lands in Recently Deleted, a built-in undo | Staged | Automation permission | S | P2 |
| Audio convert and info (**Revisited**) | "Convert this WAV to M4A" | `convert_audio`, `audio_info` (filesystem) | `afconvert`, `afinfo` | New file only; undo trashes it | No | M | P2 |
| iCloud Drive status (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `brctl` (subcommands *unverified*) | Read | No | M | P2 |

## New ideas

1. **Music playback by name.** Today Music can only skip or pause whatever is playing. Searching the library and playing a named playlist is the common request. Implementation: manifest entries using a named AppleScript builder. Search text and playlist names go in as argv after `--`, never spliced into the script. Safety: add the mandatory regression test that feeds `-e` as a playlist name and asserts it is treated as data. Needs Automation permission for Music.

2. **Safari: open, close, Reading List.** Opening a URL in Safari specifically (rather than the default browser) and closing a tab by index are the natural next steps after `list_tabs`. Reuse the URL validation that `open_website` already has (http/https only). Closing is irreversible, so the preview must say so. Needs Automation permission for Safari.

3. **Mail triage that never sends.** `reply_mail` creates a draft and leaves it for the user to review, which matches the safety stance `send_mail` already takes. `mark_read` and `move_message` are reversible flag or mailbox changes. Safety: message IDs must be validated as integers, mailbox names must be passed as argv, and the draft must be saved, not sent. Needs Automation permission for Mail.

4. **Notes move and delete.** Deleting only moves a note to Recently Deleted, so it is recoverable for about 30 days (Apple's documented behaviour, not re-verified here). That makes it a low-risk addition and a good first step into the staged lane for Notes.

5. **Volume, mute and speed test.** See the table. `speed_test` needs no user text, so the generic builder is enough and has no injection surface. Volume uses a fixed script with a validated integer. Whether the volume commands need Automation permission is *unverified*.

6. **Audio conversion and info.** Mirror `convert_image`: input path, new output path, closed format list, no free-form flags, refuse to overwrite, undo moves the new file to Trash. Whether `afconvert` honours `--` is *unverified*, so reject dash-leading paths instead.

7. **Curated `defaults` keys (carried over, not yet diffed).** Dock auto-hide and size, Finder hidden files and extensions, screenshot location. Diff against the current allowlist in `preferences.json` before building.

## Landscape notes

- **Auto-discovery servers** such as `osa-mcp` ([listing](https://awesome.ecosyste.ms/projects/github.com%2Fmaycxc%2Fosa-mcp)) read each app's scripting dictionary and generate tools from it. **Reject** as a runtime design, because the tools are not reviewed in advance. **Borrow** the idea offline: use `sdef` output to draft manifest entries, then review them by hand.
- **Apple-MCPs** ([listing](https://enterprisedna.co/directories/mcp/jonathanrreed-apple-mcps)) covers Mail and other apps and shows a September 2026 update, per its directory page. Another Apple toolkit listed on [glama](https://glama.ai/mcp/servers/nbj5slaw3u) shows v2.16.7 on npm. Release details come from directory pages and are *unverified*.
- **Safari MCP servers** exist ([guide](https://mcp.directory/blog/safari-mcp-complete-guide-2026)). Third-party blogs claim Apple shipped a built-in Safari MCP server in Safari Technology Preview 247 in July 2026. This is *unverified*, as it comes from blogs and not Apple. If true, Safari page-content automation may be better left to Apple. This server should stay on tab and URL actions.
- **Generic script runners** ([macos-automator-mcp](https://github.com/steipete/macos-automator-mcp), [applescript-mcp](https://mcpservers.org/servers/github-com-joshrutkowski-applescript-mcp)) are still the broadest. **Reject** arbitrary script execution. **Borrow** the recipe library idea: each recipe can become a fixed manifest entry.
- **Positioning:** the staged, undoable and injection-hardened model is the differentiator. The way to match competitors is curated breadth in the apps people actually use.

## Bugs or reliability risks

No new confirmed defects. One dependency carried from yesterday: `docs/issues/issue-no-executor-walltime-timeout.md` should be resolved, or each new long-running op given its own time cap, before `speed_test` and audio conversion ship.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (2026-11-12 to 11-14):** still absent on today's `main`. Confirmed by grepping `internal/registry/manifests`. For `networkQuality`, web sources show a `-s` (sequential) option, `-I <interface>` and `-v`. Treat those flags as *unverified* until `man networkQuality` is read. The no-argument form is the safe default.
- **App depth (from 2026-11-12):** promoted. Music now has a concrete list (`search_music`, `play_playlist`, `set_music_volume`) and moves to P1, since it has only four ops today.
- **Audio conversion (2026-11-13/14):** unchanged, still absent.

## Sources

- [osa-mcp](https://awesome.ecosyste.ms/projects/github.com%2Fmaycxc%2Fosa-mcp)
- [Apple-MCPs listing](https://enterprisedna.co/directories/mcp/jonathanrreed-apple-mcps)
- [Safari MCP guide](https://mcp.directory/blog/safari-mcp-complete-guide-2026)
- [macos-automator-mcp](https://github.com/steipete/macos-automator-mcp)
- [applescript-mcp](https://mcpservers.org/servers/github-com-joshrutkowski-applescript-mcp)
- [networkQuality overview (TidBITS)](https://tidbits.com/2022/04/22/use-apples-networkquality-tool-to-test-internet-responsiveness/)
- Local: the `main` manifests, particularly `system.json`, `safari.json`, `music.json`, `mail.json` and `notes.json`.

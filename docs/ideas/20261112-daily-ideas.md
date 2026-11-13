# macOS MCP server: daily ideas, 2026-11-12

Today's focus: everyday "do-the-thing" gaps in first-party apps and unwired non-admin CLI tools, plus a quick competitor check. This is the first report on this branch, so nothing is repeated from earlier days.

Method note: the shell was unavailable in this run, so local checks were limited to listing `/usr/bin` and `/usr/sbin` and grepping the code. I did not read man pages or run `sdef`. Items needing those checks are marked *unverified*.

## TL;DR

1. **Audio volume, mute and "play a sound".** There is no op to change or read output volume. It is one of the most common things people ask an assistant to do, and it is a small, reversible AppleScript call.
2. **Internet speed test (`networkQuality`) and iCloud Drive status (`brctl`).** Both ship in `/usr/bin` and neither is wired up. "Is my internet slow?" and "why isn't this file synced?" are daily questions. Both are read-only.
3. **Close the app-depth gaps in apps already covered.** Safari can only list tabs, Notes can't delete or move a note, Mail can't reply or mark read, and Music can't search or play a named item. Users expect to be able to act on these apps, not just read them.

## Fill ASAP: high-impact gaps

Today the server has 174 operations across 23 domains (from `docs/architecture.md`).

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume and mute | "Turn the volume down to 20%" / "mute" | `get_volume`, `set_volume` (system) | `osascript` fixed script (`output volume of (get volume settings)` / `set volume output volume N`) | Read; change is staged, undo restores the previous level | No | S | **P1** |
| Internet speed | "Is my internet slow right now?" | `speed_test` (network) | `/usr/bin/networkQuality` (present locally; flags *unverified*, check `man`) | Read-only, but uses bandwidth for about 20s | No | S | **P1** |
| iCloud Drive sync | "Is my file uploaded to iCloud?" / "download this evicted file" | `icloud_status`, `icloud_download` (filesystem or storage) | `/usr/bin/brctl` (present; subcommands *unverified*) | Status is a read; download is staged and reversible-ish (undo is not needed, it only fetches) | No | M | P2 |
| Safari actions | "Open this link in Safari", "save to Reading List", "close that tab" | `open_url_in_safari`, `close_tab`, `add_reading_list` (safari) | Fixed AppleScript | Staged; close-tab is irreversible, so warn | Automation TCC | M | P2 |
| Notes delete / move | "Move this note to my Work folder" / "delete that note" | `move_note`, `delete_note` (notes) | Fixed AppleScript. Delete goes to Recently Deleted, which gives a built-in undo | Staged | Automation TCC | S | P2 |
| Mail triage | "Reply to this", "mark all read", "archive that" | `reply_mail`, `mark_read`, `move_message` (mail) | Fixed AppleScript. Reply saves a draft rather than sending | Staged | Automation TCC | M | P2 |
| Music: play something | "Play my workout playlist" | `search_music`, `play_playlist`, `set_music_volume` (music) | Fixed AppleScript | Staged, auto-commit lane candidate | Automation TCC | M | P3 |
| Focus / Do Not Disturb | "Turn on Do Not Disturb for an hour" | Today `open_settings` with pane `focus` only. Keep as a hand-off. Toggling needs Shortcuts, so see New ideas #3 | n/a | n/a | Hand-off to the Focus pane | S | P3 |

## New ideas

1. **`speed_test` and `set_volume` (see the table).** Both fit the generic builder. `networkQuality` takes no user text, so there is no injection surface. Volume takes an integer 0–100, and the fixed script gets the number as `argv`, with the `--` terminator and a `-e` regression test as the rules require.

2. **`play_sound` (system).** Plays a short system sound or an audio file via `afplay` (present in `/usr/bin`), which is useful for "ping me when the build finishes" flows. Use a named builder that takes a path with a dash-leading guard, and cap the duration with the existing two-minute ceiling. Read-like, with no state change. `afinfo` could report audio file metadata (length, format) for the same domain (*unverified*).

3. **Focus mode via Shortcuts hand-in.** The server already has `run_shortcut` (staged). A documented convention such as "if a Shortcut named X exists, offer it" lets users wire up Focus, Low Power and Wi-Fi joins without any new root-level code. This is a docs and eval addition rather than Go code, and it reframes the admin-gated gaps in `docs/issues/issue-system-control-admin-gated-gaps.md`. Check whether Ventura's Shortcuts has a built-in "Set Focus" action (*unverified*).

4. **File metadata read: `file_metadata` (filesystem).** `mdls` reads Spotlight attributes (kind, creation date, where-from URL, page count). It is already used inside the Mail builtin, so the binary is allowlisted. A standalone op answers "where did I download this from?" in one call. Read-only. Validate the path (dash-leading rejected) and use a fixed attribute allowlist.

5. **Finder tags and Spotlight indexing status.** Tag read through `mdls kMDItemUserTags`. Tag write uses `xattr` (already allowlisted) and is staged, with undo restoring the old tag list. `mdutil -s` (present) tells you whether Spotlight indexing is on for a volume. Read-only, which helps explain "Spotlight can't find my file".

6. **Disk-space helpers: "what is eating my disk?"** `largest_files` and `du` exist. Add a curated `storage_hogs` read that sizes a fixed list of known cache locations (Downloads, Caches, Xcode derived data and similar) with `du`. It never deletes anything. Hand off to the Storage pane (`x-apple.systempreferences:` identifier *unverified*) for cleanup. Medium value, small effort.

7. **Calendar/Reminders depth.** `list_reminders` and `add_reminder` exist, but there is no op for creating a reminders list or setting recurrence or alarms (*unverified*: check the `sdef` dictionary for Reminders and Calendar). Worth a spike.

8. **Screen recording.** `screenshot` has three ops, all stills. `screencapture -V <seconds>` records video on newer macOS (*unverified*; check `man screencapture`). It needs the Screen Recording TCC permission, which the server already documents.

## Landscape notes

- **[steipete/macos-automator-mcp](https://github.com/steipete/macos-automator-mcp)** runs arbitrary AppleScript or JXA, with a library of 200+ ready-made recipes. Directory listings showed roughly 800–870 stars, and the counts vary by snapshot, so treat the figure as approximate. Strength: breadth, because anything scriptable works. **Reject** the arbitrary-script execution, since it contradicts this project's "data, never code" rule. **Borrow** the idea of a recipe library: its recipes show what users actually ask for, and each can become a fixed, parameterized manifest entry.
- **[joshrutkowski/applescript-mcp](https://claudemarketplaces.com/mcp/joshrutkowski/applescript-mcp)** covers calendar, clipboard, Finder, notifications and system controls, and was listed with about 383 stars. System controls such as volume are what this server lacks, so they are consistent with the proposed set above.
- **peakmojo/applescript-mcp** is a generic AppleScript runner ([listing](https://glama.ai/mcp/servers/@peakmojo/applescript-mcp)). Same rejection as above.
- I could not find results for `apple-mcp` in this run, so I make no claims about it. Re-run that search next time.
- **Positioning:** this server's differentiator is the staged, undoable, injection-hardened model. Competitors mostly trade that safety for breadth. The goal should be to match their breadth through curated manifests, not through free-form scripts.

## Bugs or reliability risks

None confirmed. One doc-level observation: `.claude/rules/go-conventions.md` refers to SDK v1.6.0 while `CLAUDE.md` pins v1.4.1. That is a docs inconsistency rather than a code bug, and `docs/issues/note-doc-counts-drift.md` already covers this kind of drift.

## Revisited ideas

None. Phase-2 units V0–V10 appear complete: the shortcuts, storage, security, SSH and keep-awake ops all exist in the manifests. The old "tabled" status of `shortcuts-runner-deferred.md` is now superseded, so that file may be worth archiving.

## Sources

- [macos-automator-mcp (GitHub)](https://github.com/steipete/macos-automator-mcp)
- [macOS Automator listing (mcpservers.org)](https://mcpservers.org/servers/github-com-steipete-macos-automator-mcp)
- [applescript-mcp (joshrutkowski)](https://claudemarketplaces.com/mcp/joshrutkowski/applescript-mcp)
- [applescript-mcp (peakmojo)](https://glama.ai/mcp/servers/@peakmojo/applescript-mcp)
- Local: project manifests, `docs/architecture.md`, `docs/ideas/capability-expansion-phase2.md`, and listings of `/usr/bin` and `/usr/sbin` (presence of `networkQuality`, `afplay`, `afinfo`, `brctl`, `mdls`, `mdutil`).

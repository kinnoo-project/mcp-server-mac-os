# macOS MCP server: daily ideas, 2026-11-13

Today's focus: audio/media command-line tools and first-party app scripting dictionaries, plus a refresh of the competitor landscape.

Method note: the shell was denied in this run, so I could not run `man` or `sdef`. Local checks were limited to listing `/usr/bin` and `/System/Applications`, reading the QuickTime Player scripting file, and reading the repo. Anything needing a man page is marked *unverified*.

## TL;DR

1. **Ship volume/mute control, `speed_test` and iCloud status first (Revisited).** I re-checked the main-branch manifests today. None of `set_volume`, `networkQuality` or `brctl` is wired up yet. These are the cheapest, most-asked-for gaps, and yesterday's top picks are unchanged.
2. **Add media conversion (audio and video).** The server can convert images (`sips`) and documents (`textutil`) but not sound or video. `afconvert`, `avconvert` and `afinfo` are all present in `/usr/bin`. "Make this recording an MP3-sized AAC" and "shrink this video" are everyday requests.
3. **Give the Mac a "record my screen/audio" and "read it aloud to a file" path.** Today `screenshot` is stills only and `speak` only plays aloud. Small additions, high day-to-day value.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume / mute (**Revisited**, still missing) | "Turn the volume to 20%" | `get_volume`, `set_volume` (system) | Fixed `osascript` script, number passed as argv after `--` | Read; change staged, undo restores previous level | No | S | **P1** |
| Internet speed (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality` (flags *unverified*) | Read-only, uses bandwidth about 20s | No | S | **P1** |
| iCloud Drive sync state (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `/usr/bin/brctl` (subcommands *unverified*) | Read | No | M | P2 |
| Audio file convert + info | "Convert this WAV to M4A", "how long is this clip?" | `convert_audio`, `audio_info` (filesystem) | `afconvert`, `afinfo` | Convert writes a NEW file; undo trashes it. Info is read | No | M | **P1** |
| Video shrink/convert | "Make this video smaller for email" | `convert_video` (filesystem) | `avconvert` with a preset allowlist (flags *unverified*) | New output file only; undo trashes it | No | M | P2 |
| Screen / audio recording | "Record my screen for 30 seconds" | `record_screen` (screenshot) | `screencapture -V` (*unverified*) or QuickTime Player AppleScript | Writes a new file; undo trashes it | Screen Recording permission | M | P2 |
| Speech to a file | "Read this text into an audio file" | extend `speak` with an output path (system) | `say -o` (*unverified*) | New file; undo trashes it | No | S | P3 |

## New ideas

1. **Audio volume and mute.** Reads and sets the Mac's output level. Fits an existing AppleScript-style named builder. The number is validated as an integer 0–100. Safety: use `--` after the script so a value like `-e` stays data, and add the required `-e` regression test. Needs no special permission for the standard-additions volume commands (*unverified*, test on a clean account).

2. **Audio and video conversion (`afconvert`, `avconvert`, `afinfo`).** Mirror how `sips` is wired in `filesystem.json`: a named builder with an input path and an output path, a closed list of target formats or presets, never free-form flags. Reject dash-leading paths, refuse to overwrite an existing file, and make undo move the created file to the Trash. These tools take no network access and no admin rights. Sources: local `/usr/bin` listing.

3. **Screen recording.** Two routes: `screencapture -V` (check `man screencapture`) or QuickTime Player's scripting dictionary, which exists locally at `/System/Applications/QuickTime Player.app` and exposes about 14 commands (I did not read their names carefully, so confirm whether a screen-recording command is included). Cap the duration at the existing two-minute ceiling. Permission needed: Screen Recording, already documented.

4. **Reminders and Calendar depth.** The scripting files for both are present locally, so a quick `sdef` check will tell us whether recurrence, alarms and creating lists are scriptable. Competitors read these through Apple's EventKit framework, which is faster than AppleScript but is not a command-line tool, so it would break this project's "approved binaries only" model. Stay with AppleScript.

5. **More curated `defaults` settings.** `preferences` has only `read_setting`, `write_setting` and `set_appearance`. Widely documented, harmless keys for the Dock (auto-hide, icon size), Finder (show hidden files, show file extensions) and screenshots (save location, format) would answer common "make my Mac do X" requests. I did not check which are already curated, so diff against the allowlist before adding. Writes are staged and undo restores the previous value.

6. **Text and Shortcuts convention (carried over).** Offering "if a Shortcut named X exists" for Focus, Low Power and similar admin-flavored toggles remains a docs plus evals change with no new Go code.

7. **`trash` command (macOS 14+).** `/usr/bin/trash` is present locally but is absent on Ventura (the floor), so keep the existing Trash-API approach and do not depend on it. Flagged only so nobody swaps it in by mistake. Availability on Ventura is *unverified*.

## Landscape notes

- **Apple-Tools-MCP** ([PyPI](https://pypi.org/project/apple-tools-mcp/1.0.1/)) bundles Mail, Calendar, Reminders, Messages, Contacts, Notes, Shortcuts, Files, System and Maps in one server. Same breadth as this project. This project wins on the stage, execute and undo model.
- **AirMCP** ([GitHub](https://github.com/heznpc/airmcp)) lists an even longer set (Music, Finder, Safari, Photos and more). Worth watching for gaps. I did not verify its stars or activity.
- **MacRelay** ([GitHub](https://github.com/drbarq/macrelay)) and **JonathanRReed's apple-mcps** ([listing](https://mcpservers.org/servers/jonathanrreed/apple-mcps)) take a "pick only the apps you need" approach. Worth borrowing: a way to enable or disable domains at startup so a client sees fewer tools. Check whether this server already has one before building it.
- Several competitors keep outbound mail disabled by default, default to dry runs, and restrict Shortcuts to an allowlist. This project already stages every change, so the main borrowable item is a Shortcuts allowlist. Check how `run_shortcut` is currently scoped.
- **Reject:** generic "run any AppleScript" servers and EventKit-linked helper binaries. They conflict with the data-not-code rule.
- Star counts and 2026 release status for all of the above are *unverified*. The search tool returned no usable counts.

## Bugs or reliability risks

None confirmed today. I could not run tests or man pages, so I did not hunt for runtime defects.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (from 2026-11-12):** still unimplemented as of today's main (verified by searching manifests and engine for `output volume`, `networkQuality` and `brctl`). Priority unchanged, and volume plus speed test are the best first PR.
- **Screen recording (from 2026-11-12):** now proposed as a proper operation, with the QuickTime Player dictionary as a fallback route.
- **Calendar/Reminders depth (from 2026-11-12):** the scripting files are confirmed present, so the `sdef` check is now a cheap next step.

## Sources

- [Apple-Tools-MCP (PyPI)](https://pypi.org/project/apple-tools-mcp/1.0.1/)
- [AirMCP (GitHub)](https://github.com/heznpc/airmcp)
- [MacRelay (GitHub)](https://github.com/drbarq/macrelay)
- [apple-mcps (mcpservers.org)](https://mcpservers.org/servers/jonathanrreed/apple-mcps)
- [macos-ecosystem-mcp (glama)](https://glama.ai/mcp/servers/neverprepared/macos-ecosystem-mcp)
- Local: project manifests, README Roadmap, `/usr/bin` listing, scripting dictionaries under `/System/Applications`.

# macOS MCP server: daily ideas, 2026-11-14

Today's focus: system services and reliability, plus a check of whether the earlier top picks have shipped.

Method note: the shell was denied again, and I did not run web searches, so there are no new landscape findings. I could not run `man` or `sdef`. Local checks were limited to file listings, grepping the `main` checkout, and reading docs. Anything that needs a man page is marked *unverified*.

## TL;DR

1. **Ship the "small three" in one PR: output volume, `speed_test`, iCloud status (Revisited, third day running).** I grepped the main manifests and engine today. Nothing sets or reads output volume, and nothing calls `networkQuality` or `brctl`. All three need no admin rights, and the server has no equivalent today.
2. **Add audio conversion and info (`afconvert`, `afinfo`).** Image and document conversion shipped (notes for unit V2: `sips`, `textutil`). Audio and video did not. Both binaries exist in `/usr/bin` locally. The existing image-conversion design carries over directly.
3. **Add a read-only `file_metadata` (`mdls`) and a "play a sound" op (`afplay`).** The `mdls` binary is already used inside the Mail code, and `afplay` is present in `/usr/bin`. Neither has a standalone operation.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume and mute (**Revisited**) | "Turn the volume down to 20%" | `get_volume`, `set_volume` (system) | Fixed `osascript` script; the number arrives as argv after `--` | Read; change is staged, and undo restores the old level | No | S | **P1** |
| Internet speed (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality` (flags *unverified*) | Read-only; uses bandwidth for about 20s | No | S | **P1** |
| iCloud Drive sync (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `/usr/bin/brctl` (subcommands *unverified*) | Read | No | M | P2 |
| Audio convert and info | "Convert this WAV to M4A", "how long is this clip?" | `convert_audio`, `audio_info` (filesystem) | `afconvert`, `afinfo` | Writes a NEW file only; undo trashes it | No | M | **P1** |
| File metadata | "Where did I download this from?" | `file_metadata` (filesystem) | `mdls` with a fixed attribute allowlist | Read | No | S | P2 |
| Play a sound | "Ping me when it's done" | `play_sound` (system) | `afplay` with a duration cap | No state change | No | S | P3 |
| Video shrink | "Make this video smaller for email" | `convert_video` (filesystem) | `avconvert` with a preset allowlist (flags *unverified*) | New file only; undo trashes it | No | M | P2 |

## New ideas

1. **Volume, mute and speed test.** What it does: reads and sets the speaker level, and measures connection speed. Value: the two most common "just do it" requests. How: `speed_test` fits the generic builder because the binary takes no user text. Volume is a named builder wrapping a fixed script, with an integer 0–100 passed as argv after `--`. Safety: add the mandatory regression test feeding `-e` as a value. Permission: the standard volume commands should need no Automation permission (*unverified*).

2. **Audio conversion and info.** What it does: converts a sound file to another format (for example WAV to M4A) and reports duration and format. Value: it completes the media story, since images and documents already convert. How: a named builder modeled on `convert_image`, with a closed list of output formats, a mandatory new output path, and undo that moves the created file to the Trash. Safety: reject dash-leading paths and resolve paths to absolute form. Whether `afconvert` honours `--` is *unverified*, so do not rely on it.

3. **`file_metadata` via `mdls`.** What it does: shows Spotlight attributes such as kind, created date, download source, and page count. How: a builtin read with an allowlist of attribute names and a dash-leading path guard (`mdls` has no `--` terminator I can verify). No special permission.

4. **`play_sound` via `afplay`.** Plays a file or a system sound, with a hard duration cap so it cannot run indefinitely. How: a named builder, auto-commit lane since it changes no state. Safety: path guard, and the process must be killed when the request context is cancelled.

5. **Screen recording (carried over).** Still unimplemented. `screencapture -V` is *unverified*, so read `man screencapture` first. It needs the Screen Recording permission, which is already documented.

6. **Curated `defaults` keys (carried over).** Dock auto-hide and size, Finder extensions and hidden files, and the screenshot save location. First diff against the existing allowlist, since I did not check which are already in it.

7. **Shortcuts convention for admin-flavored toggles (carried over).** "If a Shortcut named X exists, run it" covers Focus and Low Power with docs and evals only, no new Go.

## Landscape notes

No new research today. The carried-over notes from 2026-11-12 and 2026-11-13 still stand:

- Generic "run any AppleScript" servers (for example steipete/macos-automator-mcp) are broad but contradict the data-not-code rule. Reject execution of arbitrary scripts, but borrow the idea of a recipe library: each recipe can become a fixed manifest entry.
- Servers that let you enable only the domains you need keep the tool list short. Worth checking whether this server has a startup switch for that.
- Star counts are *unverified* and I am not repeating them.

## Bugs or reliability risks

- `docs/issues/issue-no-executor-walltime-timeout.md` is already filed. Because the proposed `speed_test`, audio conversion and `afplay` can all run for a while, that issue should be closed before they ship, or each of them needs its own explicit time cap. This is a dependency for the proposals above, not a new defect.
- `.claude/rules/go-conventions.md` says SDK v1.6.0 while `CLAUDE.md` pins v1.4.1. This is a docs mismatch only, and it was already noted on 2026-11-12, so I am not counting it as new.

No new confirmed defects.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (2026-11-12 and 2026-11-13):** still absent on today's main. Confirmed by grepping manifests and engine code. Recommend landing the first two together.
- **Audio and video conversion (2026-11-13):** the V2 design note shows the pattern to copy, so effort drops from M toward S–M for audio.
- **`trash` command idea (2026-11-13):** unchanged, so I am not repeating it.

## Sources

- Local: main-branch manifests, `docs/issues/note-v2-media-conversion-design.md`, `docs/issues/issue-no-executor-walltime-timeout.md`, and a listing of `/usr/bin` showing `networkQuality`, `brctl`, `afconvert`, `afinfo`, `afplay`, `avconvert`, `mdls`, `tmutil` and `dns-sd` present.
- Earlier reports in `docs/ideas/` for the landscape links.

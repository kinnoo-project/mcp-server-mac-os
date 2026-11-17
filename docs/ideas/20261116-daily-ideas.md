# macOS MCP server: daily ideas, 2026-11-16

Today's focus: everyday file and Finder gaps (what a user does after "find it"), plus the landscape for Focus and Finder tags.

Method note: the shell was denied again, so no `man`, `sdef` or `git log`. I read the `main` manifests with Grep/Read and ran one web search. Anything needing a man page is marked *unverified*. I could not check whether `main` changed since yesterday; a manifest search shows the same gaps as before.

## TL;DR

1. **Ship the "small three" (volume, `speed_test`, iCloud status) — Revisited, fifth day running.** `system.json` and `network.json` still have no output-volume op and no `networkQuality` call. Nobody has acted on this since 11-12; if there is a reason, record it in `docs/issues/`, otherwise just build it.
2. **Add a Finder-side "after you find it" set: reveal in Finder, checksum, and Finder tags.** The filesystem domain can find, copy, move, compress and convert, but cannot show a file to the user, verify a download, or label files. These are new proposals today.
3. **Add Focus / Do Not Disturb via a Shortcuts convention.** Competitors ship it, users ask for it constantly, and no supported CLI exists. Run a user-named Shortcut; no new Go.

## Fill ASAP: high-impact gaps

| Gap | User would ask | Proposed op (domain) | Backing | Read/change (undo) | Admin? | Effort | Priority |
|---|---|---|---|---|---|---|---|
| Output volume (**Revisited**) | "Turn the volume down" | `get_volume`, `set_volume` (system) | Fixed `osascript` script, integer via argv after `--` | Staged; undo restores old level | No | S | **P1** |
| Speed test (**Revisited**) | "Is my internet slow?" | `speed_test` (network) | `/usr/bin/networkQuality`, no flags | Read, ~20s | No | S | **P1** |
| Reveal in Finder (new) | "Show me that file" | `reveal_in_finder` (filesystem) | `open -R <path>` (flag from memory, *unverified*) | Changes no files; auto-commit | No | S | **P1** |
| Checksum (new) | "Does this download match the published hash?" | `checksum` (filesystem) | `/usr/bin/shasum -a 256` with closed algorithm list | Read | No | S | P1 |
| Finder tags (new) | "Tag these invoices red" | `get_tags`, `set_tags` (filesystem) | `mdls -name kMDItemUserTags` for read; write via `xattr` on `com.apple.metadata:_kMDItemUserTags` (binary plist, hard) *unverified* | Staged; undo restores old tags | No | M | P2 |
| Focus / DND (new) | "Turn on Do Not Disturb" | `set_focus` (system) | Run a user-named Shortcut via existing `run_shortcut` path | Staged; undo runs the off Shortcut | No | S | P2 |
| What's using this disk? (new) | "Why won't it eject?" | `files_open_on_volume` (storage) | `/usr/sbin/lsof` on a mount point, fixed flags (*unverified*) | Read | No | S | P2 |
| iCloud status (**Revisited**) | "Is this file uploaded?" | `icloud_status` (filesystem) | `brctl` (*unverified*) | Read | No | M | P2 |

## New ideas

1. **`reveal_in_finder`.** After any search the natural next step is "show me". Manifest entry using the generic builder around `open`, with the path validated to exist and dash-leading paths rejected (do not rely on `open` honouring `--`). No permission needed.
2. **`checksum`.** Lets a user confirm a download is intact. Generic builder, closed algorithm enum (sha1/sha256/sha512), paths-only, reject dash-leading paths. Read-only, no permission.
3. **Finder tags.** Reading is easy through Spotlight metadata. Writing needs a binary plist inside an extended attribute, which is awkward without a shell. Recommend starting with read plus a search "files tagged Red", and treating write as a builtin builder in Go (in-process, no subprocess). Competitors treat tag-setting as a destructive tool, so keep it staged.
4. **Focus via Shortcuts.** There is no public Focus CLI. cyanheads' server needs the built-in "Set Focus" Shortcut and an exact mode name (see Landscape). Same approach here: document that the user creates Shortcuts named e.g. "Focus On", and the server runs them. Allowlist by name so arbitrary Shortcuts are not run. Needs Automation for Shortcuts.
5. **Eject helper.** `eject_volume` exists; when it fails the user is stuck. A read-only "what has files open here" gives the model something to act on, and `quit_application` already exists to follow up.
6. **Date and calendar math (`date`, `cal`).** Models are poor at "what date is next Friday?" and have no clock. A read-only `current_time` with timezone costs nearly nothing using `/bin/date` with a fixed format enum. Not proposed before. Priority P3.
7. **Property-list reader (`plutil -p`).** Read-only view of an app's Info.plist or a preferences file, useful for "what version is this app?". Path guard needed; `plutil` options *unverified*. P3.

## Landscape notes

- **Apple-Tools-MCP** ([PyPI](https://pypi.org/project/apple-tools-mcp/1.0.1/)) advertises Finder tags, recent locations and iCloud Drive awareness alongside the usual apps, and is honest about Focus being best-effort. Borrow: Finder tags and iCloud status.
- **cyanheads/macos-mcp-server** ([listing](https://awesome.ecosyste.ms/projects/github.com%2Fcyanheads%2Fmacos-mcp-server)) has a Focus tool that depends on the built-in "Set Focus" Shortcut and an exact profile name. Borrow the Shortcuts route; reject any approach that scripts System Settings' UI.
- **AirMCP** ([GitHub](https://github.com/heznpc/airmcp)) has Finder tools including `set_file_tags` marked destructive. Supports staging tag writes.
- **Apple System MCP** ([PyPI](https://pypi.org/project/apple-system-mcp/1.0.1/)) separates read from write and falls back to GUI automation. Reject the GUI fallback; it is fragile and bypasses the safety model.
- Stars, activity and release dates are *unverified*; the search returned none.

## Bugs or reliability risks

None new. The standing dependency still applies: `docs/issues/issue-no-executor-walltime-timeout.md` should be closed (or each op given its own cap) before `speed_test` and `lsof` ship.

## Revisited ideas

- **Volume, `speed_test`, iCloud status (11-12 to 11-15):** still absent on `main`. Priority unchanged; recommend one small PR for volume + `speed_test`.
- **Shortcuts convention (11-13/14):** promoted from "carried over" to a concrete `set_focus` op, because competitors now ship Focus and it is a top user request.
- **Music/Safari/Mail/Notes depth (11-15):** unchanged and still valid; not repeated here.

## Sources

- [Apple-Tools-MCP](https://pypi.org/project/apple-tools-mcp/1.0.1/)
- [Apple System MCP](https://pypi.org/project/apple-system-mcp/1.0.1/)
- [cyanheads/macos-mcp-server](https://awesome.ecosyste.ms/projects/github.com%2Fcyanheads%2Fmacos-mcp-server)
- [AirMCP](https://github.com/heznpc/airmcp)
- Local: `main` manifests `filesystem.json`, `system.json`, `network.json`, `storage.json`, `shortcuts.json`.

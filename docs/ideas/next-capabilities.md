# Next Capabilities — Candidate Roadmap

> Ten unbuilt capabilities that extend the [capability engine](macos-mcp-capability-engine.md) toward the "command-center for macOS" vision, ordered roughly by value-to-effort. Each fits the existing grain: a JSON manifest entry plus either the generic argv builder, a named builder over an allowlisted binary, or an AppleScript-backed builtin — with reads going straight through `query` and mutations routed through `plan_action` → `commit_action` → `undo_action`.

## Context

Today the server covers **filesystem, preferences, mail, calendar, reminders, phone, messages, printer, system, and a generic application launcher**. The defensible value is the safe-mutation layer (stage → execute → undo, policy allowlist, AppleScript option-injection hardening), so new capabilities are most valuable where they (a) give the agent new *senses or reach* the Bash tool can't safely provide, and (b) have a clean inverse so they slot into the undo model.

This note is a backlog, not a commitment. Implementation order being pursued: this note → Notes → Screenshot → Notifications/TTS → Process monitoring → Network diagnostics → Disk/storage.

---

## 1. Clipboard (`pbpaste` / `pbcopy`)

**What:** `read_clipboard` and `write_clipboard`. High-leverage glue — hand text to whatever app the user is in, and read what they just copied.

**How:** Stable binaries `/usr/bin/pbpaste` and `/usr/bin/pbcopy`. Read is a trivial read-only builtin. Write is the interesting one: `pbcopy` reads its content from **stdin**, not argv — the first capability that needs the executor to feed stdin (small, generally useful extension if not already present). Treat write as mutating; the natural undo is restoring the prior clipboard, captured by reading `pbpaste` at stage time.

**Watch out:** Clipboards hold huge or binary payloads (images, RTF) — apply the 8KB truncation rule on read and flag non-text content. Reading can surface secrets (password-manager copies); note that in the operation description.

## 2. Notes (`application-notes`)

**What:** The conspicuous gap in the Apple-app lineup. `list_notes`, `search_notes`, `read_note`, `create_note` / `append_to_note`.

**How:** Exactly the mail/messages/reminders pattern — AppleScript builtins driving the Notes app, with the mandatory `--` end-of-options terminator on `osascript` and a flag-like-subject regression test. Reads go straight through; creates stage → execute.

**Watch out:** Note bodies are HTML under AppleScript — strip/flatten to text on read, mirroring the Messages `attributedBody` recovery. Locked/encrypted notes are inaccessible and must fail cleanly. Folder/account targeting for new notes needs an explicit param or a documented default.

## 3. Screenshot / screen capture (`screencapture`)

**What:** `capture_screen`, `capture_window` (by app/window), `capture_region`. Gives a vision-capable agent eyes on the actual screen.

**How:** `/usr/sbin/screencapture` with positional flags into a named builder, output to a controlled file path (return the path, or read back as an image resource if the MCP layer supports image content). Route output through a temp/fallback dir convention rather than a model-chosen destination.

**Watch out:** Requires the **Screen Recording** TCC permission — handle silent-failure / black-image with a clear "grant Screen Recording" message. Avoid interactive modes (`-i`, `-s`); there's no human at the prompt in an agent loop. Strictly allowlist flags — `screencapture` has many.

## 4. Shortcuts runner (`shortcuts`)

**What:** `list_shortcuts` and `run_shortcut <name> [input]`. Force-multiplier: the sanctioned automation surface for things with no clean CLI on modern macOS — Focus modes, HomeKit, system toggles.

**How:** `/usr/bin/shortcuts` (Ventura+, our floor). `list` is read-only; `run "<name>"` is mutating because a shortcut can do anything — stage → execute with the resolved name in the preview. Input via stdin or `--input-path`.

**Watch out:** Effectively an arbitrary-action escape hatch — risk-score "run unknown shortcut" as high, and there's no meaningful auto-undo, so lean on the preview/confirm step. Dash-leading names must be rejected or `--`-guarded.

## 5. Window & app-state management (System Events / Accessibility)

**What:** `list_windows`, `frontmost_app`, `move_window`, `resize_window`, `minimize` / `fullscreen`. Arrange the workspace, not just launch apps.

**How:** AppleScript against `System Events`. Reads (window list, frontmost) are safe; geometry changes are mutating, and undo is clean and worth implementing — capture prior `{position, size}` at stage time, restore on undo.

**Watch out:** Requires the **Accessibility** TCC permission; detect/report the permission errors distinctly. Same `osascript` `--` hardening. Window coordinates differ across multi-monitor setups — validate bounds.

## 6. Network diagnostics & info (`networksetup`, `scutil`, `dig`, `ping`)

**What:** Extends `system` beyond `wifi_status`: `network_services`, `current_ip`, `dns_servers`, `ping_host`, `dns_lookup`, optionally `set_dns` (mutating).

**How:** Reads via `/usr/sbin/networksetup -listallnetworkservices`, `scutil --dns`, `ipconfig getifaddr en0`. `ping`/`dig` take a host arg. `set_dns` is mutating with a natural undo (re-apply captured prior server list).

**Watch out:** `ping`/`dig` accept a model-controlled host — an SSRF/exfiltration channel and a prime **dash-leading injection** target (e.g. host `-f` to flood). Apply §4 host validation hard: allowlist hostname/IP shapes, reject metacharacters, bound packet count. Many `networksetup` writes need admin rights — detect/report the privilege failure rather than hang.

## 7. Volume, brightness & media control

**What:** `get_volume` / `set_volume` / `mute`, and Now-Playing controls (`play_pause`, `next_track`, `now_playing`) for Music/Spotify.

**How:** Volume is simple via AppleScript — `set volume output volume N` and `output volume of (get volume settings)` — no special permission; set is mutating with trivial undo (prior level). Media control is AppleScript against the Music/Spotify app.

**Watch out:** Brightness has **no stable built-in CLI** on Apple Silicon (legacy `brightness` is third-party) — scope it out or gate behind Shortcuts (#4). Media ops require the target app running/installed; fail cleanly. Validate volume to 0–100.

## 8. Process & resource monitoring (`ps`, `vm_stat`, `kill`)

**What:** `list_processes` (CPU/mem, name filter), `process_info`, and — carefully — `quit_process`.

**How:** `/bin/ps -Ao pid,pcpu,pmem,comm` parsed and sorted is a clean read-only builtin (great for "what's eating my battery/CPU"). `vm_stat` / `top -l 1` for system totals.

**Watch out:** Killing a process is **irreversible** — it doesn't fit the undo model, so either omit it or make it stage-only with a strong confirm, preferring a graceful AppleScript `quit` over `kill -9`. Validate PID/name inputs (no signal-number injection via a dash-leading PID). Apply output truncation hard — process lists are long.

## 9. Notifications & text-to-speech (`osascript display notification`, `say`)

**What:** `notify` (banner the user) and `speak` (read text aloud). The agent → human back-channel for "long-running task finished" without watching the terminal.

**How:** `display notification "..." with title "..."` via AppleScript needs no extra permission and is low-risk. `say` is `/usr/bin/say` with text as a positional arg (or `-f` from a file for long text). Both are fire-and-forget; no undo.

**Watch out:** Both take model-controlled strings into `osascript`/argv — the `--` terminator (osascript) and dash-leading guard (`say`) are mandatory, plus a flag-like-text regression test. `say` on a huge string blocks; cap length and respect context cancellation so a dropped request stops the speech.

## 10. Disk & storage info (`df`, `diskutil list`, `du`)

**What:** `disk_usage` (free/used per volume), `list_volumes`, a bounded `directory_size`.

**How:** `/bin/df -H` and `/usr/sbin/diskutil list` are read-only and parse cleanly into a builtin. `directory_size` via `du -sh <path>` reuses existing filesystem path validation.

**Watch out:** `du` over a large tree is slow and unbounded — tie strictly to request context for cancellation, consider a depth/time cap. Reuse filesystem root/path guards so the path argument can't be dash-led or escape into flag territory.

---

## Cross-cutting notes

- **TCC permissions are the recurring gotcha.** Screen Recording (#3), Accessibility (#5), and the Automation/FDA prompts already handled each fail *silently or with cryptic codes*. Every new permission-gated capability should map its error code to a plain "grant X in System Settings → Privacy & Security" message, consistent with existing permission handling.
- **The `osascript` `--` terminator + flag-like-value regression test is non-negotiable** for #2, #5, #7, #9 — four new AppleScript surfaces, four new injection tests.
- **Pick the right builder tier:** clipboard/process/disk/network reads are clean generic-or-named builders over allowlisted binaries; the Apple-app ones (#2, #5, #7 media, #9) are AppleScript builtins like the existing mail/messages code.
- **Undo discipline:** clipboard, window geometry, volume, and DNS all have clean inverse states worth capturing at stage time. Screenshot, shortcut-run, kill, ping, notify/speak have **no real undo** — be honest about that in the manifest and lean on the stage/confirm preview instead of pretending a rollback exists.

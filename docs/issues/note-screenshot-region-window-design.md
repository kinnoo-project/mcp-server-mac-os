**note**

Unit 12 adds two cropped captures to the existing `screenshot` domain (no new MCP
tool): `capture_region` (an explicit rectangle) and `capture_window` (a single
app's window). Both are read-only/low and, like `capture_screen`, run immediately.

## One shared spine, three entry points

The three screenshot builtins now funnel into a single `runCapture` helper
(`builtins_screenshot.go`). It owns everything that must behave identically no
matter what is being photographed: resolving and guarding the output path,
enforcing the **create-only, never-overwrite** contract that keeps a capture in
the read-only lane, running `screencapture`, and turning a silent Screen
Recording denial (an empty or missing file, often with exit 0) into an actionable
"grant Screen Recording" message. Each entry point differs only in how it derives
the optional `-R x,y,w,h` crop:

- `capture_screen` — no crop (`region == nil`); may pass `-D` to pick a display.
- `capture_region` — the crop comes straight from four validated ints.
- `capture_window` — the crop comes from a window's probed on-screen bounds.

Because the path-guarding, overwrite refusal, and permission-error handling live
in one place, they are written and tested once rather than three times.

## Why capture_window reads bounds instead of using -l

`screencapture` has a native window mode (`-l <windowid>`), but it needs a
**CGWindowID**, and AppleScript / System Events cannot produce one — it only
exposes a window's position and size. So `capture_window` reuses Unit 11's
System Events geometry read (`probeWindowGeometry`, `mutate_windowing.go`) to get
the window's rectangle, then captures that rectangle with `-R`. For a normal,
unobscured window the visible result is identical. The trade-off is that `-R`
photographs whatever is **on screen** at those coordinates, so an overlapping
window would appear in the shot; the manifest summary says so plainly. A window
reported as zero-size (a fully minimized or off-screen window) is rejected with a
clear message rather than producing an empty image the create-only spine would
then misread as a permission failure.

## Fail-fast ordering (capture_window)

Because the window-bounds read is permission-gated, `runCapture` defers it behind
a `regionFn` callback and runs it only *after* the cheap, non-prompting validation
(format, `output_path` dash-leading guard, no-overwrite check) has passed. So a
request with an obviously-invalid `output_path` is rejected before any System
Events call — it never provokes a spurious Automation/Accessibility prompt for a
request that was going to fail on its path anyway.
`TestCaptureWindow_RejectsDashLeadingOutputPathFast` pins this ordering.

## Permissions

- `capture_region` needs only **Screen Recording** (same as `capture_screen`).
- `capture_window` additionally needs **Accessibility** + **Automation**, because
  reading the window's bounds goes through System Events. A missing window-read
  grant surfaces via `windowScriptError` (Unit 11) *before* any capture is
  attempted, so the message points at the right Settings pane.

## Injection

Both new ops carry free-text parameters and are `reviewedFreeTextBuiltins`
entries. `output_path` is rejected if dash-leading (the shared
`resolveScreenshotPath` guard) and is only ever used to *create* a file. For
`capture_window`, the `app` name is validated by `validateAppNameValue` and
reaches the geometry-probe script only as inert argv data after the `--`
terminator. The region ints are validated whole numbers, never free text.
`capture_region` reuses `positiveDimension` (from the window mutators) so width
and height are bounded exactly as window sizes are.

**fixed**
Implemented in Unit 12 (`feat/screenshot-region-window`). Wiring verified by the
engine unit tests plus the server's `ValidateBuilders`/`Register`/menu tests;
in-session `runevals` for the two new routing cases is blocked by the stale
running MCP binary (a recurring situation across units — the binary was rebuilt
into `bin/` for the next session). Live capture paths are covered by the
`MCP_SCREENSHOT_LIVE=1`-gated tests and the `m_screenshot_region` /
`m_screenshot_window` manual smoke cases.

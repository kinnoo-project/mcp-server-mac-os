// builtins_windowing.go implements the read-only window inspection builtin
// (list_windows) that the window-management mutators (mutate_windowing.go) act on.
//
// Windows are enumerated through the macOS Accessibility API, which System Events
// exposes to AppleScript. This is the same "what is on screen right now" read
// that move_window / resize_window / minimize_window mutate, so the addressing
// model is shared: a window belongs to a foreground *process* and is identified
// by its 1-based front-to-back index. Because this reaches into other apps'
// UI, it requires BOTH Automation access (to talk to System Events) AND
// Accessibility access (assistive access to read window geometry); a missing
// grant is mapped to an actionable hint by windowScriptError (mutate_windowing.go).
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// windowFieldSep is the delimiter listWindowsScript emits between a row's fields.
// It is the ASCII Unit Separator (0x1f) — the same choice asFieldSep makes for
// single-record probes — because a window TITLE is arbitrary user text that can
// contain tabs or other punctuation, and only a separator that cannot plausibly
// appear inside a title keeps the FIELD split unambiguous. (The ROW split is a
// linefeed, so listWindowsScript additionally flattens any tab/return/linefeed
// inside a title to spaces before emitting it, so a title with an embedded
// newline cannot split a row.) The AppleScript side emits it as `(character id 31)`.
const windowFieldSep = "\x1f"

// listWindowsScript emits one row per on-screen window as
// `process<US>title<US>x<US>y<US>width<US>height`. When argv item 1 is empty it
// walks every foreground (non-background-only) process; otherwise it narrows to
// the single process of that exact name. The name arrives as data bound to
// `on run argv` after the "--" terminator, so a flag-like value can never be read
// as an osascript option.
//
// Each window's geometry read is wrapped in a `try` so a single window that
// refuses the position/size query (some accessory or borderless windows do)
// degrades to "?" placeholders rather than aborting the whole listing.
const listWindowsScript = `on run argv
	set target to item 1 of argv
	set sep to (character id 31)
	set out to ""
	tell application "System Events"
		if target is "" then
			set procs to (every process whose background only is false)
		else
			set procs to (every process whose name is target)
		end if
		repeat with p in procs
			set pname to name of p
			repeat with w in (windows of p)
				set wtitle to ""
				try
					set wtitle to name of w
				end try
				if wtitle is missing value then set wtitle to ""
				-- Flatten any tab/return/linefeed inside the title to single spaces
				-- so an embedded newline in a window title cannot split the
				-- one-row-per-window output the Go parser relies on (mirrors _clean).
				set savedTID to AppleScript's text item delimiters
				set AppleScript's text item delimiters to {tab, return, linefeed}
				set wtitleParts to text items of (wtitle as string)
				set AppleScript's text item delimiters to " "
				set wtitle to wtitleParts as string
				set AppleScript's text item delimiters to savedTID
				set px to "?"
				set py to "?"
				set pw to "?"
				set ph to "?"
				try
					set pos to position of w
					set px to (item 1 of pos) as string
					set py to (item 2 of pos) as string
					set sz to size of w
					set pw to (item 1 of sz) as string
					set ph to (item 2 of sz) as string
				end try
				set out to out & pname & sep & wtitle & sep & px & sep & py & sep & pw & sep & ph & linefeed
			end repeat
		end repeat
	end tell
	return out
end run`

// runListWindows lists the on-screen windows of foreground applications,
// optionally narrowed to a single named app.
//
// The optional "app" filter is validated through validateAppNameValue (the same
// guard the window mutators use): a dash-leading or control-character value is
// rejected up front so the failure is a clear validation error, even though the
// name would in any case reach the script only as inert argv data after "--".
func runListWindows(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	appFilter := ""
	if raw, ok := getString(in, "app"); ok && strings.TrimSpace(raw) != "" {
		validated, err := validateAppNameValue("list_windows", "app", raw)
		if err != nil {
			return "", err
		}
		appFilter = validated
	}

	res, err := runOsascript(ctx, listWindowsScript, appFilter)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", windowScriptError("list_windows", res.Stderr)
	}
	return renderWindowList(res.Stdout, appFilter), nil
}

// renderWindowList is the pure formatting half of runListWindows, split out so the
// row parsing and empty/narrowed messaging can be unit-tested without a live
// System Events call. It takes the script's raw unit-separated rows and the app
// filter that produced them (used only to word the "no windows" message), and
// returns a bounded, human-readable listing.
func renderWindowList(stdout, appFilter string) string {
	rows := asRows(stdout)
	if len(rows) == 0 {
		if appFilter != "" {
			return fmt.Sprintf("No open windows found for %q (it may not be running, or may have no windows).", appFilter)
		}
		return "No open windows found."
	}

	var b strings.Builder
	if appFilter != "" {
		fmt.Fprintf(&b, "%d open window(s) for %q:\n", len(rows), appFilter)
	} else {
		fmt.Fprintf(&b, "%d open window(s):\n", len(rows))
	}
	for _, row := range rows {
		f := strings.Split(row, windowFieldSep)
		// Rows are always emitted with six fields; anything else is malformed
		// output we render verbatim rather than mis-parse.
		if len(f) != 6 {
			fmt.Fprintf(&b, "  %s\n", row)
			continue
		}
		proc, title, x, y, w, h := f[0], f[1], f[2], f[3], f[4], f[5]
		if strings.TrimSpace(title) == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "  %s — %q at (%s, %s), %s×%s\n", proc, title, x, y, w, h)
	}
	return compactOutput(b.String())
}

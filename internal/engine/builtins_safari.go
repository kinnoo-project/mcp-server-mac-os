// builtins_safari.go implements the read-only Safari.app operations that let the
// assistant answer "what do I have open in Safari?" and "what page am I looking
// at?" — list_tabs and current_tab — by driving Safari through its AppleScript
// dictionary via the hardened osascript seam in applescript.go, then formatting
// the delimited stdout into human text in Go.
//
// # Why these are AppleScript builtins
//
// Safari exposes its open windows, tabs, and the active ("current") tab only
// through its scripting dictionary; there is no CLI that reads them. So — like
// the Mail, Notes, and Calendar reads — the work is a FIXED AppleScript program
// plus result parsing, and lives here as an in-process builtin rather than a
// generic argv builder.
//
// # Guardrails
//
//   - Neither operation takes a free-text parameter: list_tabs accepts only an
//     optional 1-based window index (a validated integer) and current_tab takes
//     nothing. An integer cannot carry an injection payload, so there is no
//     dash-leading value to neutralize and no reviewedFreeTextBuiltins entry is
//     required. The value still reaches the script strictly as DATA bound to
//     `on run argv`, after the "--" terminator runOsascript inserts (see
//     applescript.go), so the "data, never code" property holds structurally.
//   - Each tab's title is flattened with _clean so a page title containing a tab
//     or newline cannot corrupt the one-row-per-line output the Go parser relies
//     on; a tab with no URL yet (the Start Page / Favorites) coerces to an empty
//     string via _str rather than erroring.
//   - A denied Automation permission (the first call prompts for control of
//     Safari) surfaces as an actionable hint via safariScriptError rather than a
//     bare AppleScript error number.
//
// # Privacy
//
// Open URLs reveal what the user is browsing — genuinely personal content. The
// manifest descriptions flag that so a caller invokes these deliberately, and
// list_tabs' output is bounded by the shared compactOutput budget so a session
// with very many tabs cannot saturate the model's context.
//
// # Explicit non-goal: no `do JavaScript`
//
// Safari can run arbitrary JavaScript in a tab via `do JavaScript`, which would
// let the assistant read full page CONTENT. That path is intentionally NOT
// implemented: it is gated behind Safari's Develop-menu "Allow JavaScript from
// Apple Events" setting, is effectively remote code execution against whatever
// page is loaded, and is far outside the read-the-tab-list scope of this
// capability. These builtins read only tab metadata (title + URL).
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// listTabsScript emits one tab-delimited row per open Safari tab:
//
//	windowIndex \t title \t url
//
// argv (after the "--" terminator): windowFilter. An empty windowFilter lists
// every window's tabs; a non-empty value is the 1-based index of the single
// window to list. Windows are numbered in front-to-back order (window 1 is
// frontmost), matching Safari's own `windows` ordering.
//
// Not every Safari window has tabs — a non-browsing window (e.g. a Settings or
// downloads window) has no `tabs` property and would error on access — so the
// `tabs of theWin` read is wrapped in a `try` that treats such a window as
// empty. Helper calls are prefixed with `my` so they resolve to this script's
// _clean/_str handlers rather than Safari's dictionary.
const listTabsScript = asDateHelpers + `on run argv
	set winFilter to item 1 of argv
	set out to ""
	tell application "Safari"
		set wins to windows
		set winCount to (count of wins)
		repeat with wi from 1 to winCount
			if winFilter is "" or winFilter is (wi as string) then
				set theWin to item wi of wins
				try
					set theTabs to tabs of theWin
				on error
					set theTabs to {}
				end try
				repeat with ti from 1 to (count of theTabs)
					set t to item ti of theTabs
					set out to out & wi & tab & my _clean(name of t) & tab & my _str(URL of t) & linefeed
				end repeat
			end if
		end repeat
	end tell
	return out
end run`

// currentTabScript returns the front window's active tab as title <sep> url,
// joined by asFieldSep (0x1f) so the two fields split cleanly even though a
// title can contain arbitrary characters. It errors with a clear message when no
// Safari window is open so the Go side can distinguish "nothing open" from a
// permission failure.
const currentTabScript = asDateHelpers + `on run argv
	set sep to (character id 31)
	tell application "Safari"
		if (count of windows) is 0 then error "no Safari windows are open"
		set theTab to current tab of front window
		set theTitle to my _clean(name of theTab)
		set theURL to my _str(URL of theTab)
	end tell
	return theTitle & sep & theURL
end run`

// safariTab is one parsed row from listTabsScript: which window it belongs to
// (1-based) plus its title and URL.
type safariTab struct {
	window     int
	title, url string
}

// runListTabs lists Safari's open tabs (title + URL), grouped by window, across
// every window or scoped to a single 1-based window index.
func runListTabs(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	// The optional window index is a plain integer; validate it is positive so a
	// nonsensical value fails as a clear caller error rather than silently
	// listing nothing. An empty filter (omitted param) lists all windows.
	windowFilter := ""
	if w, ok := getInt(in, "window"); ok {
		if w < 1 {
			return "", fmt.Errorf("list_tabs: 'window' must be a positive 1-based index (window 1 is frontmost)")
		}
		windowFilter = itoa(w)
	}

	res, err := runOsascript(ctx, listTabsScript, windowFilter)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", safariScriptError("list_tabs", res.Stderr)
	}

	tabs := parseTabRows(res.Stdout)
	if len(tabs) == 0 {
		if windowFilter != "" {
			return fmt.Sprintf("No open tabs found in Safari window %s (it may not exist or may be a non-browsing window).", windowFilter), nil
		}
		return "No open tabs found in Safari (it may not be running, or has no windows open).", nil
	}
	return compactOutput(renderTabList(tabs)), nil
}

// runCurrentTab returns the title and URL of the active tab in Safari's front
// window.
func runCurrentTab(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, currentTabScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		// "no windows" is a benign, common state (Safari open with no window, or
		// not running); report it plainly rather than as a permission hint.
		if strings.Contains(res.Stderr, "no Safari windows are open") {
			return "", fmt.Errorf("current_tab: no Safari windows are open")
		}
		return "", safariScriptError("current_tab", res.Stderr)
	}
	// title <sep> url; SplitN keeps a title containing anything (except the 0x1f
	// separator, which a title never does) intact. osascript prints one trailing
	// newline after the returned string; strip exactly that one.
	parts := strings.SplitN(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep, 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("current_tab: unexpected probe output")
	}
	title, url := parts[0], parts[1]
	return fmt.Sprintf("%s\n%s", orUntitled(title), orUnknown(url)), nil
}

// parseTabRows splits the tab-delimited rows from listTabsScript into safariTab
// values. A row with the wrong field count or a non-numeric window index is
// skipped defensively so one odd record can't corrupt the whole listing (mirrors
// parseInboxRows/parseNoteRows).
func parseTabRows(stdout string) []safariTab {
	var out []safariTab
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 3 {
			continue
		}
		win, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		out = append(out, safariTab{window: win, title: f[1], url: f[2]})
	}
	return out
}

// renderTabList formats the tabs under a heading, grouped by window with a
// per-window subheading, one tab per line as "title  —  url". Tabs arrive in
// window order from the script, so a single pass emits a new "Window N:" header
// whenever the window index changes.
func renderTabList(tabs []safariTab) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d open tab(s) in Safari:\n", len(tabs))
	lastWindow := 0
	n := 0
	for _, t := range tabs {
		if t.window != lastWindow {
			fmt.Fprintf(&b, "Window %d:\n", t.window)
			lastWindow = t.window
			n = 0
		}
		n++
		fmt.Fprintf(&b, "  %2d. %s  —  %s\n", n, orUntitled(t.title), orUnknown(t.url))
	}
	return b.String()
}

// safariScriptError wraps an osascript failure with a hint about the most common
// cause — Automation access to Safari not yet granted — so the caller gets an
// actionable message instead of a bare AppleScript error number. Mirrors
// mailScriptError/notesScriptError; Safari is a new Automation permission
// surface (no other capability drives it).
func safariScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Safari automation failed (%s). If this is the first use, macOS may need you to grant this app access to Safari in System Settings → Privacy & Security → Automation", op, stderr)
}

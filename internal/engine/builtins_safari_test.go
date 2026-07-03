// builtins_safari_test.go tests the pure, in-Go logic of the Safari read
// builtins (list_tabs, current_tab): parsing the AppleScript's delimited stdout,
// grouping tabs by window when rendering, and the window-index validation guard.
//
// SAFETY: nothing here launches osascript or touches the real Safari app — these
// tests feed the parser/renderer the kind of rows the script emits, so they read
// no real browsing history and require no Automation grant.
//
// The osascript "--"-terminator hardening is the SAME osascriptArgv seam
// exercised by TestOsascriptArgv_SharedByBothPaths (applescript_test.go); the
// dedicated case below re-asserts it for the Safari scripts to document that even
// the (integer) window filter reaches the script strictly as data.
package engine

import (
	"strings"
	"testing"
)

// TestParseTabRows verifies well-formed tab-delimited rows are parsed and that a
// malformed row (wrong field count) or a row with a non-numeric window index is
// skipped rather than corrupting the result.
func TestParseTabRows(t *testing.T) {
	stdout := strings.Join([]string{
		"1\tExample Domain\thttps://example.com/",
		"1\tSearch\thttps://example.com/search?q=go",
		"malformed-row-without-tabs",
		"x\tBad window index\thttps://example.com/bad", // non-numeric window → skipped
		"2\tSecond window tab\thttps://example.org/",
		"", // blank line dropped by asRows
	}, "\n")

	got := parseTabRows(stdout)
	if len(got) != 3 {
		t.Fatalf("expected 3 parsed rows (malformed + bad-index skipped), got %d: %+v", len(got), got)
	}
	if got[0].window != 1 || got[0].title != "Example Domain" || got[0].url != "https://example.com/" {
		t.Errorf("first row parsed wrong: %+v", got[0])
	}
	if got[2].window != 2 || got[2].title != "Second window tab" {
		t.Errorf("third row parsed wrong: %+v", got[2])
	}
}

// TestRenderTabList verifies the listing groups tabs under a per-window heading,
// restarts the per-window numbering at each new window, and uses placeholders for
// an empty title or URL.
func TestRenderTabList(t *testing.T) {
	out := renderTabList([]safariTab{
		{window: 1, title: "First", url: "https://example.com/a"},
		{window: 1, title: "", url: ""}, // empty fields → placeholders
		{window: 2, title: "Third", url: "https://example.org/"},
	})
	for _, want := range []string{
		"3 open tab(s) in Safari:",
		"Window 1:",
		"Window 2:",
		"First",
		"(untitled)",
		"(unknown)",
		"https://example.org/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered list missing %q:\n%s", want, out)
		}
	}
	// The second window's single tab must be numbered 1, not 3 — numbering resets
	// per window.
	if !strings.Contains(out, "   1. Third") && !strings.Contains(out, "1. Third") {
		t.Errorf("expected window 2's tab renumbered to 1, got:\n%s", out)
	}
}

// TestRunListTabs_RejectsNonPositiveWindow verifies list_tabs refuses a zero or
// negative window index BEFORE it would ever reach osascript, so a caller mistake
// is a clear input error rather than an opaque empty listing.
func TestRunListTabs_RejectsNonPositiveWindow(t *testing.T) {
	c := lookupCapability(t, "list_tabs")
	for _, w := range []int{0, -1, -100} {
		if _, err := runListTabs(nil, c, map[string]any{"window": w}); err == nil {
			t.Errorf("expected an error for window %d, got nil", w)
		}
	}
}

// TestSafariScripts_UseOptionTerminator documents the structural "data, never
// code" guarantee: the argv both Safari scripts run through always carries the
// "--" end-of-options terminator before any data value, so even a window filter
// that somehow looked flag-like could never be parsed by osascript as one of its
// own options. (The window filter is a validated integer, so this is
// belt-and-suspenders — the terminator is shared with every other osascript
// builtin.)
func TestSafariScripts_UseOptionTerminator(t *testing.T) {
	for _, script := range []string{listTabsScript, currentTabScript} {
		argv := osascriptArgv(script, "1")
		term := indexOf(argv, "--")
		data := indexOf(argv, "1")
		if term < 0 {
			t.Fatalf("osascript argv missing '--' terminator: %v", argv)
		}
		if data >= 0 && data < term {
			t.Errorf("data value appears before '--' terminator: %v", argv)
		}
	}
}

// builtins_windowing_test.go covers the pure formatting and input-guard halves of
// the list_windows builtin without a live System Events call: renderWindowList's
// row parsing/messaging, and runListWindows's rejection of an unsafe app filter.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// row builds a single list_windows script row (unit-separated fields) the way
// listWindowsScript emits them, so renderWindowList tests read as real output.
func row(proc, title, x, y, w, h string) string {
	return strings.Join([]string{proc, title, x, y, w, h}, windowFieldSep)
}

func TestRenderWindowList_FormatsRows(t *testing.T) {
	stdout := row("Safari", "Apple", "100", "80", "1200", "800") + "\n" +
		row("Notes", "", "0", "0", "640", "480") + "\n"

	got := renderWindowList(stdout, "")

	if !strings.Contains(got, "2 open window(s):") {
		t.Errorf("missing count header:\n%s", got)
	}
	if !strings.Contains(got, `Safari — "Apple" at (100, 80), 1200×800`) {
		t.Errorf("Safari row not rendered as expected:\n%s", got)
	}
	// A blank title is shown as (untitled) rather than empty quotes.
	if !strings.Contains(got, `Notes — "(untitled)" at (0, 0), 640×480`) {
		t.Errorf("untitled Notes row not rendered as expected:\n%s", got)
	}
}

func TestRenderWindowList_NarrowedEmpty(t *testing.T) {
	got := renderWindowList("", "Safari")
	if !strings.Contains(got, `No open windows found for "Safari"`) {
		t.Errorf("narrowed empty message wrong: %q", got)
	}

	got = renderWindowList("", "")
	if got != "No open windows found." {
		t.Errorf("unfiltered empty message wrong: %q", got)
	}
}

// TestRenderWindowList_MalformedRowRenderedVerbatim ensures a row that does not
// have exactly six fields is not silently mis-parsed but printed as-is.
func TestRenderWindowList_MalformedRowRenderedVerbatim(t *testing.T) {
	got := renderWindowList("weird-line-no-separators\n", "")
	if !strings.Contains(got, "weird-line-no-separators") {
		t.Errorf("malformed row not rendered verbatim: %q", got)
	}
}

// TestListWindows_RejectsUnsafeAppFilter is the injection regression for the
// list_windows free-text "app" parameter: a dash-leading value (the classic
// osascript option-injection payload) is rejected by validateAppNameValue before
// any osascript is spawned, so it never reaches the script even as data. Because
// validation fails first, this needs no live System Events call.
func TestListWindows_RejectsUnsafeAppFilter(t *testing.T) {
	for _, bad := range []string{"-e", "-flag", "bad\x00name", "ctrl\x01char"} {
		_, err := runListWindows(context.Background(), registry.Capability{}, map[string]any{"app": bad})
		if err == nil {
			t.Errorf("app filter %q: expected a validation error, got nil", bad)
		}
	}
}

// TestListWindows_AppFilterIsInertData proves the structural guard behind the
// filter: whatever the app value, osascriptArgv places it after the "--"
// terminator, so even a flag-like value reaches the script as data bound to
// `on run argv` rather than being parsed as an osascript option.
func TestListWindows_AppFilterIsInertData(t *testing.T) {
	for _, h := range hostileValues {
		argv := osascriptArgv(listWindowsScript, h)
		want := []string{"-e", listWindowsScript, "--", h}
		if len(argv) != len(want) {
			t.Fatalf("%q: argv = %q, want %q", h, argv, want)
		}
		for i := range want {
			if argv[i] != want[i] {
				t.Errorf("%q: argv[%d] = %q, want %q", h, i, argv[i], want[i])
			}
		}
	}
}

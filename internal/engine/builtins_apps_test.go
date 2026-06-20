// builtins_apps_test.go tests the pure formatting half of the application
// listing builtins (renderAppList): de-duplication by name, case-insensitive
// substring filtering, alphabetical ordering, and truncation. It never launches
// mdfind or osascript — it feeds synthetic mdfind output straight to the renderer.
package engine

import (
	"strings"
	"testing"
)

// sampleMdfind is a stand-in for mdfind's newline-separated app-bundle paths,
// including a duplicate (Safari indexed twice) to exercise de-duplication.
const sampleMdfind = `/Applications/Safari.app
/System/Applications/Notes.app
/System/Applications/Reminders.app
/Applications/Safari.app
`

func TestRenderAppList_DedupesAndSorts(t *testing.T) {
	out := renderAppList(sampleMdfind, "", 100)
	// Three distinct apps despite four input lines.
	if !strings.Contains(out, "Found 3 installed application(s):") {
		t.Errorf("expected a count of 3 distinct apps, got: %s", out)
	}
	// Alphabetical order: Notes before Reminders before Safari.
	notes := strings.Index(out, "Notes")
	reminders := strings.Index(out, "Reminders")
	safari := strings.Index(out, "Safari")
	if !(notes < reminders && reminders < safari) {
		t.Errorf("apps not sorted alphabetically: %s", out)
	}
}

func TestRenderAppList_Filters(t *testing.T) {
	out := renderAppList(sampleMdfind, "REMIND", 100) // case-insensitive
	if !strings.Contains(out, "Reminders") {
		t.Errorf("filter should match Reminders: %s", out)
	}
	if strings.Contains(out, "Safari") || strings.Contains(out, "Notes") {
		t.Errorf("filter should exclude non-matches: %s", out)
	}
	if !strings.Contains(out, `matching "remind"`) {
		t.Errorf("expected the filter echoed in the header: %s", out)
	}
}

func TestRenderAppList_NoMatch(t *testing.T) {
	if out := renderAppList(sampleMdfind, "zzz-nothing", 100); !strings.Contains(out, "No installed applications match") {
		t.Errorf("expected a clean no-match message, got: %s", out)
	}
	if out := renderAppList("", "", 100); !strings.Contains(out, "No installed applications found.") {
		t.Errorf("expected an empty-result message, got: %s", out)
	}
}

func TestRenderAppList_Truncates(t *testing.T) {
	out := renderAppList(sampleMdfind, "", 2)
	if !strings.Contains(out, "Found 3 installed application(s) (showing the first 2):") {
		t.Errorf("expected truncation notice keeping the true total, got: %s", out)
	}
	// Sorted then truncated → Notes and Reminders shown, Safari dropped.
	if strings.Contains(out, "Safari") {
		t.Errorf("Safari should have been truncated away: %s", out)
	}
}

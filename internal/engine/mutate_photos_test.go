// mutate_photos_test.go tests the subprocess-free logic of the reversible Photos
// mutators: the pure forward/inverse command builders (including the option-
// injection regression that proves a flag-like value lands as "--"-terminated
// data), and the validation/parse paths that fail BEFORE any live probe.
//
// SAFETY: the full stage path of each mutator runs a live Photos probe to capture
// prior state (exactly like append_to_note), so — as in mutate_notes_test.go —
// only the pure command builders and the pre-probe validation are unit-tested
// here; the forward/inverse SHAPE is asserted via the builders they are made of.
package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestPhotoCommandBuilders_TerminateHostileData is the option-injection regression
// for every reversible Photos mutator's AppleScript surface: each command builder
// must place "--" before its data, so a flag-like id or value (e.g. "-e") reaches
// the script as `on run argv` data, never as an osascript flag.
func TestPhotoCommandBuilders_TerminateHostileData(t *testing.T) {
	hostile := "-e"
	checks := []struct {
		name string
		cmd  Command
		data []string // values that must each appear after the "--" terminator
	}{
		{"favorite", favoriteCommand(hostile, hostile), []string{hostile}},
		{"name", nameCommand(hostile, hostile), []string{hostile}},
		{"description", descriptionCommand(hostile, hostile), []string{hostile}},
		{"date", dateCommand(hostile, ymdhm{2024, 7, 4, 9, 30}), []string{hostile, "2024", "7", "4", "9", "30"}},
		{"keywords", keywordsCommand(hostile, []string{hostile, "--flood"}), []string{hostile, "--flood"}},
	}
	for _, c := range checks {
		if c.cmd.Binary != "osascript" {
			t.Errorf("%s: binary = %q, want osascript", c.name, c.cmd.Binary)
			continue
		}
		term := indexOf(c.cmd.Args, "--")
		if term < 0 {
			t.Errorf("%s: no '--' terminator in argv %q", c.name, c.cmd.Args)
			continue
		}
		// The script source sits before the terminator; every data value after it.
		for _, d := range c.data {
			if !appearsAfter(c.cmd.Args, d, term) {
				t.Errorf("%s: data %q does not appear after the terminator in argv %q", c.name, d, c.cmd.Args)
			}
		}
	}
}

// TestKeywordsCommand_EmptyClears verifies clearing keywords stages as id-only argv
// (after the terminator), which the script reads as an empty replacement list.
func TestKeywordsCommand_EmptyClears(t *testing.T) {
	cmd := keywordsCommand("ABC123", nil)
	want := []string{"-e", setKeywordsScript, "--", "ABC123"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("empty keywordsCommand argv = %v, want %v", cmd.Args, want)
	}
}

// TestStageSetDate_RejectsBadDateBeforeProbe verifies a malformed date is refused
// by the pure parse step, so no live Photos probe is attempted.
func TestStageSetDate_RejectsBadDateBeforeProbe(t *testing.T) {
	cap := lookupCapability(t, "set_date")
	for _, bad := range []string{"2024-13-01", "07/04/2024", "not-a-date", ""} {
		_, err := stageSetDate(context.Background(), cap, map[string]any{"id": "ABC123", "date": bad})
		if err == nil {
			t.Errorf("stageSetDate should reject date %q", bad)
		}
	}
}

// TestPhotoMutators_RequireID verifies every reversible mutator refuses a missing
// id before doing anything else.
func TestPhotoMutators_RequireID(t *testing.T) {
	stagers := map[string]Mutator{
		"set_favorite":    stageSetFavorite,
		"set_title":       stageSetTitle,
		"set_description": stageSetDescription,
		"set_date":        stageSetDate,
		"set_keywords":    stageSetKeywords,
	}
	for name, stage := range stagers {
		_, err := stage(context.Background(), registry.Capability{}, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "'id' is required") {
			t.Errorf("%s should require id, got err=%v", name, err)
		}
	}
}

// TestKeywordsPreviewAndQuoteOrCleared covers the preview helpers' empty/non-empty
// branches.
func TestKeywordsPreviewAndQuoteOrCleared(t *testing.T) {
	if got := keywordsPreview(nil); got != "(none)" {
		t.Errorf("keywordsPreview(nil) = %q, want (none)", got)
	}
	if got := keywordsPreview([]string{"a", "b"}); got != "[a, b]" {
		t.Errorf("keywordsPreview = %q, want [a, b]", got)
	}
	if got := quoteOrCleared(""); got != "(cleared)" {
		t.Errorf("quoteOrCleared(empty) = %q, want (cleared)", got)
	}
	if got := quoteOrCleared("hi"); got != `"hi"` {
		t.Errorf("quoteOrCleared(hi) = %q", got)
	}
}

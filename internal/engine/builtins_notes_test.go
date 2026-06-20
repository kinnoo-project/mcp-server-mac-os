// builtins_notes_test.go tests the pure, in-Go logic of the Notes read builtins:
// parsing the AppleScript scripts' delimited stdout, ordering notes newest-first,
// capping output, and rendering. The osascript "--"-terminator hardening that
// guards the search-term/id inputs is the SAME runOsascript seam exercised by
// TestOsascriptCommand_InsertsTerminator (applescript_test.go) and the
// create_note flag-like regression (mutate_notes_test.go), so it is not
// re-driven here.
//
// SAFETY: nothing here launches osascript or touches the real Notes app — these
// tests feed the parser/renderer the kind of rows the scripts emit.
package engine

import (
	"strings"
	"testing"
)

// TestParseNoteRows verifies well-formed tab-delimited rows are parsed and that a
// row with the wrong field count is skipped rather than corrupting the result.
func TestParseNoteRows(t *testing.T) {
	stdout := strings.Join([]string{
		"x-coredata://AAA/p1\tGroceries\t2026-06-19 09:30\tShopping",
		"malformed-row-without-tabs",
		"x-coredata://BBB/p2\tIdeas\t2026-06-20 14:05\tNotes",
		"", // blank line dropped by asRows
	}, "\n")

	got := parseNoteRows(stdout)
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed rows (malformed skipped), got %d: %+v", len(got), got)
	}
	if got[0].id != "x-coredata://AAA/p1" || got[0].title != "Groceries" ||
		got[0].modified != "2026-06-19 09:30" || got[0].folder != "Shopping" {
		t.Errorf("first row parsed wrong: %+v", got[0])
	}
}

// TestSortAndCapNotes verifies notes come back most-recently-modified first and
// are trimmed to the limit (keeping the newest, not an arbitrary slice).
func TestSortAndCapNotes(t *testing.T) {
	notes := []noteMeta{
		{id: "a", modified: "2026-01-01 08:00"},
		{id: "b", modified: "2026-06-20 14:05"},
		{id: "c", modified: "2026-03-15 12:00"},
	}
	got := sortAndCapNotes(notes, 2)
	if len(got) != 2 {
		t.Fatalf("expected cap to 2, got %d", len(got))
	}
	if got[0].id != "b" || got[1].id != "c" {
		t.Errorf("expected newest-first [b c], got [%s %s]", got[0].id, got[1].id)
	}
}

// TestRenderNoteList verifies the listing exposes each note's id (what
// read_note/append_to_note consume) and uses placeholders for empty fields.
func TestRenderNoteList(t *testing.T) {
	out := renderNoteList("1 note(s):", []noteMeta{
		{id: "x-coredata://AAA/p1", title: "", modified: "2026-06-20 14:05", folder: ""},
	})
	for _, want := range []string{"x-coredata://AAA/p1", "(untitled)", "(unknown)", "modified 2026-06-20 14:05"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered list missing %q:\n%s", want, out)
		}
	}
}

// TestCappedNoteLimit covers the default, the hard ceiling, and a non-positive
// value falling back to the default.
func TestCappedNoteLimit(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want int
	}{
		{map[string]any{}, defaultNoteLimit},
		{map[string]any{"limit": 10}, 10},
		{map[string]any{"limit": 999}, maxNoteLimit},
		{map[string]any{"limit": 0}, defaultNoteLimit},
		{map[string]any{"limit": -5}, defaultNoteLimit},
	}
	for _, c := range cases {
		if got := cappedNoteLimit(c.in); got != c.want {
			t.Errorf("cappedNoteLimit(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

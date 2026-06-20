// mutate_notes_test.go tests create_note's and append_to_note's validation, argv
// construction, HTML-body composition, and previews.
//
// SAFETY: no test executes a StagedPlan, so no real note is ever created or
// modified. append_to_note's stage path runs a live Notes probe (to capture the
// prior body), so only create_note's stage path and the pure HTML/validation
// helpers are unit-tested here; the append forward/inverse shape is asserted via
// the helpers it is built from.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func createNoteCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "create_note")
}

// TestStageCreateNote_ForwardAndInverse checks the staged create builds the
// expected forward (folder + HTML body) and inverse (delete-by-title) commands.
func TestStageCreateNote_ForwardAndInverse(t *testing.T) {
	plan, err := stageCreateNote(context.Background(), createNoteCapability(t), map[string]any{
		"title": "Groceries", "body": "milk\neggs", "folder": "Shopping",
	})
	if err != nil {
		t.Fatalf("stageCreateNote: %v", err)
	}
	if plan.Inverse == nil {
		t.Fatal("create_note must be reversible: Inverse should be non-nil")
	}

	fa := plan.Forward.Args
	// ["-e", createNoteScript, "--", folder, bodyHTML]
	if plan.Forward.Binary != "osascript" || len(fa) != 5 || fa[1] != createNoteScript {
		t.Fatalf("unexpected forward argv: %v", fa)
	}
	if fa[2] != "--" {
		t.Errorf("missing osascript terminator at index 2: %v", fa)
	}
	if fa[3] != "Shopping" {
		t.Errorf("folder not in expected position: %v", fa)
	}
	// First line is the bold title; body lines are preserved as <br>.
	if want := "<div><b>Groceries</b></div><div>milk<br>eggs</div>"; fa[4] != want {
		t.Errorf("body HTML = %q, want %q", fa[4], want)
	}

	ia := plan.Inverse.Args
	// ["-e", deleteNoteByTitleScript, "--", folder, title]
	if len(ia) != 5 || ia[1] != deleteNoteByTitleScript || ia[2] != "--" || ia[3] != "Shopping" || ia[4] != "Groceries" {
		t.Fatalf("unexpected inverse argv: %v", ia)
	}

	for _, want := range []string{"Groceries", "Shopping", "Undo will delete"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

// TestStageCreateNote_DefaultFolder verifies an omitted folder stages as an empty
// folder argument (the script's "default folder" branch) and reads clearly in the
// preview.
func TestStageCreateNote_DefaultFolder(t *testing.T) {
	plan, err := stageCreateNote(context.Background(), createNoteCapability(t), map[string]any{
		"title": "Idea",
	})
	if err != nil {
		t.Fatalf("stageCreateNote: %v", err)
	}
	if plan.Forward.Args[3] != "" {
		t.Errorf("default folder should stage as empty string, got %q", plan.Forward.Args[3])
	}
	// Title-only note carries just the bold title line, no body div.
	if want := "<div><b>Idea</b></div>"; plan.Forward.Args[4] != want {
		t.Errorf("title-only body HTML = %q, want %q", plan.Forward.Args[4], want)
	}
	if !strings.Contains(plan.Preview, "default folder") {
		t.Errorf("preview should mention the default folder: %s", plan.Preview)
	}
}

// TestStageCreateNote_FlagLikeTitleStaysData is the option-injection regression
// for the new AppleScript surface: a title of "-e" must land as data after the
// "--" terminator in BOTH the forward and the inverse, never as an osascript flag.
func TestStageCreateNote_FlagLikeTitleStaysData(t *testing.T) {
	plan, err := stageCreateNote(context.Background(), createNoteCapability(t), map[string]any{
		"title": "-e",
	})
	if err != nil {
		t.Fatalf("stageCreateNote: %v", err)
	}
	if plan.Forward.Args[2] != "--" || !strings.Contains(plan.Forward.Args[4], "-e") {
		t.Errorf("flag-like title not neutralized in forward: %v", plan.Forward.Args)
	}
	if plan.Inverse.Args[2] != "--" || plan.Inverse.Args[4] != "-e" {
		t.Errorf("flag-like title not neutralized in inverse: %v", plan.Inverse.Args)
	}
}

func TestStageCreateNote_Rejects(t *testing.T) {
	cases := map[string]map[string]any{
		"no title":    {"body": "orphan body"},
		"blank title": {"title": "   "},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageCreateNote(context.Background(), createNoteCapability(t), params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestStageAppendToNote_Rejects verifies append_to_note rejects a missing id and
// empty-or-whitespace-only text BEFORE it would run the live Notes probe, so a
// no-op append (which would add an empty block and mislead the preview) can never
// be staged. These cases fail at validation, so no real note is read or touched.
func TestStageAppendToNote_Rejects(t *testing.T) {
	cap := lookupCapability(t, "append_to_note")
	cases := map[string]map[string]any{
		"no id":           {"text": "something"},
		"blank id":        {"id": "   ", "text": "something"},
		"no text":         {"id": "x-coredata://abc"},
		"empty text":      {"id": "x-coredata://abc", "text": ""},
		"whitespace text": {"id": "x-coredata://abc", "text": "   \n\t "},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageAppendToNote(context.Background(), cap, params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestNoteBodyHTML_Escaping verifies HTML metacharacters in the title/body are
// escaped (so they render as text, not markup) and newlines become <br>.
func TestNoteBodyHTML_Escaping(t *testing.T) {
	got := noteBodyHTML("a<b>&\"c\"", "1 < 2\n3 & 4")
	want := "<div><b>a&lt;b&gt;&amp;&#34;c&#34;</b></div><div>1 &lt; 2<br>3 &amp; 4</div>"
	if got != want {
		t.Errorf("noteBodyHTML escaping:\n got %q\nwant %q", got, want)
	}
}

// TestAppendedHTML_Escaping verifies appended text is escaped and wrapped in its
// own block.
func TestAppendedHTML_Escaping(t *testing.T) {
	if got, want := appendedHTML("x & y\nz"), "<div>x &amp; y<br>z</div>"; got != want {
		t.Errorf("appendedHTML = %q, want %q", got, want)
	}
}

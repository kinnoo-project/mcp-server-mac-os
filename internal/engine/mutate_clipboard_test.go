// mutate_clipboard_test.go tests write_clipboard's pure staging logic through
// planWriteClipboard: forward/inverse command layout, the two no-undo cases, the
// oversized-text rejection, and the security guarantee that model-supplied text
// travels as inert stdin data with no argv surface. None of these run pbcopy or
// touch the live clipboard — the prior contents are supplied directly.
package engine

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlanWriteClipboard_ReversibleWhenPriorTextPresent(t *testing.T) {
	plan, err := planWriteClipboard("new value", []byte("old value"))
	if err != nil {
		t.Fatalf("planWriteClipboard: %v", err)
	}
	// Forward: pbcopy with the new text on stdin and NO argv operand — the text
	// is data, never a flag or path.
	if plan.Forward.Binary != "pbcopy" {
		t.Errorf("forward binary = %q, want pbcopy", plan.Forward.Binary)
	}
	if len(plan.Forward.Args) != 0 {
		t.Errorf("forward should have no argv operand (text travels on stdin), got %v", plan.Forward.Args)
	}
	if string(plan.Forward.Stdin) != "new value" {
		t.Errorf("forward stdin = %q, want the new text verbatim", plan.Forward.Stdin)
	}
	if !plan.Forward.DiscardStdout {
		t.Error("forward should discard stdout")
	}
	// Inverse: pbcopy restoring the prior text byte-exact.
	if plan.Inverse == nil {
		t.Fatal("expected an inverse when the prior clipboard held text")
	}
	if plan.Inverse.Binary != "pbcopy" || len(plan.Inverse.Args) != 0 {
		t.Errorf("inverse should be pbcopy with no argv, got %q %v", plan.Inverse.Binary, plan.Inverse.Args)
	}
	if string(plan.Inverse.Stdin) != "old value" {
		t.Errorf("inverse stdin = %q, want the prior text verbatim", plan.Inverse.Stdin)
	}
	if !plan.Inverse.DiscardStdout {
		t.Error("inverse should discard stdout so undo never echoes the prior (possibly secret) clipboard")
	}
	if !strings.Contains(plan.Preview, "restore") {
		t.Errorf("preview should mention the undo restore, got %q", plan.Preview)
	}
}

// TestPlanWriteClipboard_NoUndoWhenPriorEmpty proves that when the prior
// clipboard yielded no text (empty or non-text like an image), the plan offers
// NO inverse rather than a lossy "clear the clipboard" that could not bring an
// image back.
func TestPlanWriteClipboard_NoUndoWhenPriorEmpty(t *testing.T) {
	plan, err := planWriteClipboard("something", []byte{})
	if err != nil {
		t.Fatalf("planWriteClipboard: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("expected no inverse when the prior clipboard was empty/non-text")
	}
	if !strings.Contains(plan.Preview, "Undo is not available") {
		t.Errorf("preview should explain undo is unavailable, got %q", plan.Preview)
	}
}

// TestPlanWriteClipboard_NoUndoWhenPriorOversized declines undo when the prior
// clipboard is larger than the byte cap kept for restoration.
func TestPlanWriteClipboard_NoUndoWhenPriorOversized(t *testing.T) {
	prior := bytes.Repeat([]byte("a"), maxClipboardBytes+1)
	plan, err := planWriteClipboard("small", prior)
	if err != nil {
		t.Fatalf("planWriteClipboard: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("expected no inverse when the prior clipboard exceeds the undo cap")
	}
	if !strings.Contains(plan.Preview, "Undo is not available") {
		t.Errorf("preview should explain undo is unavailable, got %q", plan.Preview)
	}
}

// TestPlanWriteClipboard_RejectsOversizedText refuses to stage a write whose new
// text exceeds the cap, before any command is assembled.
func TestPlanWriteClipboard_RejectsOversizedText(t *testing.T) {
	text := strings.Repeat("z", maxClipboardBytes+1)
	if _, err := planWriteClipboard(text, []byte("prior")); err == nil {
		t.Fatal("expected an error for text exceeding the size limit")
	}
}

// TestPlanWriteClipboard_HostileTextLandsAsData is the injection regression: a
// value that would be a flag to some binary (e.g. "-e") or shell-active in a
// shell context must reach pbcopy only as stdin bytes, never as an argv element.
// Because pbcopy takes no operand at all, the text has no argv path to begin
// with; this test pins that the assembled command keeps it purely on stdin.
func TestPlanWriteClipboard_HostileTextLandsAsData(t *testing.T) {
	for _, hostile := range []string{"-e", "--", "-rf", "; rm -rf /", "$(reboot)", "`reboot`"} {
		plan, err := planWriteClipboard(hostile, []byte("prior"))
		if err != nil {
			t.Fatalf("%q: planWriteClipboard: %v", hostile, err)
		}
		if len(plan.Forward.Args) != 0 {
			t.Errorf("%q: hostile text must not appear in argv; got args %v", hostile, plan.Forward.Args)
		}
		if string(plan.Forward.Stdin) != hostile {
			t.Errorf("%q: hostile text must land verbatim on stdin, got %q", hostile, plan.Forward.Stdin)
		}
	}
}

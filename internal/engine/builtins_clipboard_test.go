// builtins_clipboard_test.go tests read_clipboard's pure rendering half. The
// empty/non-empty branching and the output size-bounding are exercised directly
// through formatClipboardText, so no test here reads the machine's real
// clipboard — a live pbpaste read could surface a user's actual (possibly
// secret) clipboard contents, which these tests deliberately avoid.
package engine

import (
	"strings"
	"testing"
)

func TestFormatClipboardText_Empty(t *testing.T) {
	out := formatClipboardText("")
	if !strings.Contains(out, "empty") {
		t.Errorf("expected an empty-clipboard note, got %q", out)
	}
}

func TestFormatClipboardText_ReturnsTextVerbatim(t *testing.T) {
	out := formatClipboardText("hello world")
	if out != "hello world" {
		t.Errorf("expected the clipboard text unchanged, got %q", out)
	}
}

// TestFormatClipboardText_BoundsLargeOutput confirms a clipboard larger than the
// subprocess output budget is compacted rather than returned whole — a builtin's
// output is not otherwise capped, so this is the only thing keeping a giant paste
// from flooding the model's context.
func TestFormatClipboardText_BoundsLargeOutput(t *testing.T) {
	huge := strings.Repeat("x", maxOutputBytes+5000)
	out := formatClipboardText(huge)
	if len(out) >= len(huge) {
		t.Errorf("expected output to be compacted below the input length %d, got %d", len(huge), len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected a truncation notice in the compacted output, got %q", out[:80])
	}
}

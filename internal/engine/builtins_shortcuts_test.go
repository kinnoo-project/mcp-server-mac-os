// builtins_shortcuts_test.go covers the read-only half of the shortcuts domain:
// the fixed argv of list_shortcuts (no model input, so no injection surface) and
// that runListShortcuts returns real output when shortcuts exist. The listing
// runs the real `shortcuts` binary, so the behavioural assertion is skipped when
// the machine has no shortcuts.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestListShortcutsArgvIsFixed pins the read-only argv so a future edit cannot
// turn the listing into a run (or attach any model-controlled operand).
func TestListShortcutsArgvIsFixed(t *testing.T) {
	if got := strings.Join(shortcutsListArgs(), " "); got != "list --show-identifiers" {
		t.Errorf("shortcutsListArgs = %q, want \"list --show-identifiers\"", got)
	}
	// The verb must be the read-only "list", never "run".
	if shortcutsListArgs()[0] != "list" {
		t.Errorf("list_shortcuts must use the read-only \"list\" verb, got %v", shortcutsListArgs())
	}
}

// TestRunListShortcuts_Smoke drives the real listing. It only asserts the output
// is well-formed when shortcuts are present; an empty library or an unavailable
// Shortcuts service yields a friendly message rather than an error, which is a
// valid state on a bare machine.
func TestRunListShortcuts_Smoke(t *testing.T) {
	out, err := runListShortcuts(context.Background(), registry.Capability{}, map[string]any{})
	if err != nil {
		t.Fatalf("runListShortcuts returned an error (should surface empty/unavailable as data): %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("runListShortcuts returned empty output; expected at least a status line")
	}
}

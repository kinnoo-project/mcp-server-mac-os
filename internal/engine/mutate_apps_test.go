// mutate_apps_test.go tests the application mutators' validation and command
// construction, including the option-injection regression mandated by CLAUDE.md
// §4: a flag-like application name must be refused, and a normal name must land
// as DATA after the osascript "--" terminator, never as a flag.
//
// SAFETY: no test executes a StagedPlan, so no app is ever launched, focused, or
// quit. stageOpenApplication's success path is intentionally NOT unit-tested
// because it runs a live System Events probe (which can block on an Automation
// permission prompt); only its pre-probe validation is exercised here. The
// forward/inverse construction is instead covered through focus/quit, which take
// the same name through the same osascript path.
package engine

import (
	"context"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// dataAfterTerminator returns the argv element immediately following the "--"
// end-of-options marker in an osascript Command — i.e. the first value osascript
// will expose to the script as `on run argv`. It fails the test if there is no
// terminator or nothing after it.
func dataAfterTerminator(t *testing.T, c Command) string {
	t.Helper()
	if c.Binary != "osascript" {
		t.Fatalf("expected osascript command, got %q", c.Binary)
	}
	for i, a := range c.Args {
		if a == "--" {
			if i+1 >= len(c.Args) {
				t.Fatalf("nothing follows the -- terminator in %v", c.Args)
			}
			return c.Args[i+1]
		}
	}
	t.Fatalf("no -- terminator in %v", c.Args)
	return ""
}

func TestValidateAppName(t *testing.T) {
	// Valid names pass through trimmed.
	if name, err := validateAppName("op", map[string]any{"name": "  Safari  "}); err != nil || name != "Safari" {
		t.Errorf("validateAppName(Safari) = %q, %v; want Safari", name, err)
	}
	// Rejected: empty, leading dash (flag-like), control characters.
	for _, bad := range []any{"", "   ", "-e", "--", "Bad\tName", "Bad\nName"} {
		if _, err := validateAppName("op", map[string]any{"name": bad}); err == nil {
			t.Errorf("validateAppName(%q) should have failed", bad)
		}
	}
}

// TestStageFocus_NameIsData is the option-injection regression for the focus
// path: a normal name lands after "--", and the activate command is irreversible.
func TestStageFocus_NameIsData(t *testing.T) {
	plan, err := stageFocusApplication(context.Background(), focusCapability(t), map[string]any{"name": "Safari"})
	if err != nil {
		t.Fatalf("stageFocusApplication: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("focus_application must be irreversible: Inverse should be nil")
	}
	if got := dataAfterTerminator(t, plan.Forward); got != "Safari" {
		t.Errorf("app name landed as %q, want it as data after -- (Safari)", got)
	}
}

// TestStageFocus_RejectsFlagLikeName confirms a "-e" name is refused before any
// command is built — the value can never become an osascript flag.
func TestStageFocus_RejectsFlagLikeName(t *testing.T) {
	if _, err := stageFocusApplication(context.Background(), focusCapability(t), map[string]any{"name": "-e"}); err == nil {
		t.Error("a flag-like app name (-e) should be rejected")
	}
}

func TestStageQuit_StagedIrreversible(t *testing.T) {
	plan, err := stageQuitApplication(context.Background(), quitCapability(t), map[string]any{"name": "Safari"})
	if err != nil {
		t.Fatalf("stageQuitApplication: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("quit_application offers no automatic undo: Inverse should be nil")
	}
	if got := dataAfterTerminator(t, plan.Forward); got != "Safari" {
		t.Errorf("app name landed as %q, want it as data after -- (Safari)", got)
	}
}

// TestStageOpen_RejectsBadName exercises open_application's validation without
// reaching its live probe (the bad name errors out first).
func TestStageOpen_RejectsBadName(t *testing.T) {
	if _, err := stageOpenApplication(context.Background(), openCapability(t), map[string]any{"name": "-e"}); err == nil {
		t.Error("a flag-like app name (-e) should be rejected before launch")
	}
	if _, err := stageOpenApplication(context.Background(), openCapability(t), map[string]any{"name": ""}); err == nil {
		t.Error("an empty app name should be rejected")
	}
}

func focusCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "focus_application")
}
func quitCapability(t *testing.T) registry.Capability { return lookupCapability(t, "quit_application") }
func openCapability(t *testing.T) registry.Capability { return lookupCapability(t, "open_application") }

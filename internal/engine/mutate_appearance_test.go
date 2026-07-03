// mutate_appearance_test.go exercises set_appearance's staging logic. It never
// runs the forward/inverse (which would flip the developer's actual appearance)
// and never invokes the live System Events probe (which is Automation-gated and
// unavailable in CI): the pure plan-builder planSetAppearance and the mode/enum
// handling are tested directly, and the osascript "--" hardening is asserted on
// the assembled commands.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestPlanSetAppearance_ForwardInverse confirms the forward sets the requested
// mode and the inverse restores the prior one, both as osascript commands whose
// data argument sits AFTER a "--" terminator (option-injection hardening).
func TestPlanSetAppearance_ForwardInverse(t *testing.T) {
	// Requesting dark from a prior light state.
	plan := planSetAppearance(true /* wantDark */, false /* priorDark */)

	assertAppearanceCommand(t, "forward", plan.Forward, "true")
	if plan.Inverse == nil {
		t.Fatal("set_appearance must be reversible: inverse is nil")
	}
	assertAppearanceCommand(t, "inverse", *plan.Inverse, "false")

	for _, want := range []string{"dark mode", "currently light mode", "back to light mode"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %q", want, plan.Preview)
		}
	}
}

// TestPlanSetAppearance_RestoresPriorDark confirms that when the prior state is
// dark, the inverse restores dark (baked at stage time), not a hardcoded value.
func TestPlanSetAppearance_RestoresPriorDark(t *testing.T) {
	plan := planSetAppearance(false /* wantDark */, true /* priorDark */)
	assertAppearanceCommand(t, "forward", plan.Forward, "false")
	assertAppearanceCommand(t, "inverse", *plan.Inverse, "true")
}

// assertAppearanceCommand checks an appearance command is a hardened osascript
// invocation whose sole data argument is the expected boolean token placed after
// the "--" end-of-options terminator.
func assertAppearanceCommand(t *testing.T, which string, cmd Command, wantBool string) {
	t.Helper()
	if cmd.Binary != "osascript" {
		t.Errorf("%s: binary = %q, want osascript", which, cmd.Binary)
	}
	// osascriptCommand layout: -e <script> -- <data...>
	want := []string{"-e", setDarkModeScript, "--", wantBool}
	if len(cmd.Args) != len(want) {
		t.Fatalf("%s: argv = %q, want %q", which, cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("%s: argv[%d] = %q, want %q", which, i, cmd.Args[i], want[i])
		}
	}
}

// TestStageSetAppearance_RejectsUnknownMode confirms an out-of-enum mode is
// refused before any System Events probe runs (the switch precedes probeDarkMode),
// so this test needs no Automation grant.
func TestStageSetAppearance_RejectsUnknownMode(t *testing.T) {
	cap := registry.Capability{
		Name:          "set_appearance",
		Category:      "preferences",
		Binary:        "osascript",
		Reversibility: registry.Reversible,
		Risk:          registry.RiskLow,
		AutoCommit:    true,
		Builder:       "set_appearance",
		Params: []registry.ParamSpec{
			{Name: "mode", Type: registry.TypeString, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
		},
	}
	_, err := stageSetAppearance(context.Background(), cap, map[string]any{"mode": "sepia"})
	if err == nil {
		t.Fatal("expected stageSetAppearance to reject an unknown mode")
	}
	// Pin that this is the intended short-circuit (the mode switch, which precedes
	// any System Events probe) and not some other unexpected failure: the message
	// names the offending mode.
	if !strings.Contains(err.Error(), "unknown mode") || !strings.Contains(err.Error(), "sepia") {
		t.Errorf("expected an 'unknown mode \"sepia\"' rejection, got: %v", err)
	}
}

// TestSystemEventsScriptError_MentionsAutomation confirms the shared TCC helper
// points the user at the Automation pane, so a first-use permission denial reads
// as an actionable instruction rather than a bare AppleScript error.
func TestSystemEventsScriptError_MentionsAutomation(t *testing.T) {
	err := systemEventsScriptError("set_appearance", "Not authorized to send Apple events to System Events. (-1743)")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"System Events", "Automation", "Privacy & Security"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %q", want, msg)
		}
	}
}

// mutate_system_test.go tests open_settings' pane→URL mapping and command
// construction. No plan is executed, so no Settings window is ever opened.
package engine

import (
	"context"
	"strings"
	"testing"
)

func TestStageOpenSettings_BuildsOpenURL(t *testing.T) {
	plan, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": "printers"})
	if err != nil {
		t.Fatalf("stageOpenSettings: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("open_settings has no undo: Inverse should be nil")
	}
	if plan.Forward.Binary != "open" || len(plan.Forward.Args) != 1 {
		t.Fatalf("Forward = %s %v, want a single-arg open command", plan.Forward.Binary, plan.Forward.Args)
	}
	if !strings.HasPrefix(plan.Forward.Args[0], "x-apple.systempreferences:") {
		t.Errorf("expected a System Settings URL, got %q", plan.Forward.Args[0])
	}
}

// TestStageOpenSettings_EveryPaneHasURL confirms every pane the manifest enum
// admits resolves to a URL, so the registry/engine drift guard in the server
// package is backed by an engine-side completeness check too.
func TestStageOpenSettings_EveryPaneHasURL(t *testing.T) {
	for _, pane := range SettingsPaneKeys() {
		plan, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": pane})
		if err != nil {
			t.Errorf("pane %q: %v", pane, err)
			continue
		}
		if plan.Forward.Args[0] == "" {
			t.Errorf("pane %q produced an empty URL", pane)
		}
	}
}

func TestStageOpenSettings_RejectsUnknownPane(t *testing.T) {
	if _, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": "nonsense"}); err == nil {
		t.Error("an unknown pane should be rejected")
	}
}

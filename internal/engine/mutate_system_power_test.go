// mutate_system_power_test.go tests display_sleep and wifi_set_power command
// construction. No plan is ever executed, so no display sleeps and no Wi-Fi
// radio is toggled; the stateful probe (networksetup -getairportpower) is
// bypassed by testing the pure planWifiSetPower / parseWifiPower halves directly.
package engine

import (
	"context"
	"strings"
	"testing"
)

// --- display_sleep ----------------------------------------------------------

// TestStageDisplaySleep_BuildsPmsetCommand checks the forward command layout: a
// fixed `pmset displaysleepnow`, an irreversible plan (nil Inverse), and a
// preview that explains waking is a manual act without repeating the server's
// own "cannot be undone" suffix.
func TestStageDisplaySleep_BuildsPmsetCommand(t *testing.T) {
	plan, err := stageDisplaySleep(context.Background(), lookupCapability(t, "display_sleep"), nil)
	if err != nil {
		t.Fatalf("stageDisplaySleep: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("display_sleep is irreversible: Inverse should be nil")
	}
	if !strings.Contains(plan.Preview, "manual") {
		t.Errorf("preview should say waking is a manual action, got %q", plan.Preview)
	}
	// The auto-commit path appends "This cannot be undone."; the preview must not
	// duplicate that phrase (matching notify/speak/open_settings).
	if strings.Contains(plan.Preview, "cannot be undone") {
		t.Errorf("preview must not repeat the server's 'cannot be undone' suffix, got %q", plan.Preview)
	}
	want := []string{"displaysleepnow"}
	if plan.Forward.Binary != "pmset" || len(plan.Forward.Args) != len(want) || plan.Forward.Args[0] != want[0] {
		t.Fatalf("forward = %s %q, want pmset %q", plan.Forward.Binary, plan.Forward.Args, want)
	}
}

// --- wifi_set_power ---------------------------------------------------------

// TestPlanWifiSetPower_ForwardAndInverse pins the argv layout for both toggle
// directions: the forward sets the requested state, and the inverse restores the
// state observed at stage time (baked in, not recomputed at undo).
func TestPlanWifiSetPower_ForwardAndInverse(t *testing.T) {
	cases := []struct {
		name        string
		want, prior string
		wantForward string
		wantInverse string
	}{
		{"turn off while on", "off", "on", "off", "on"},
		{"turn on while off", "on", "off", "on", "off"},
		{"turn on while already on", "on", "on", "on", "on"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planWifiSetPower("en0", tc.want, tc.prior)

			wantFwd := []string{"-setairportpower", "en0", tc.wantForward}
			if plan.Forward.Binary != "networksetup" || !equalStrings(plan.Forward.Args, wantFwd) {
				t.Errorf("forward = %s %q, want networksetup %q", plan.Forward.Binary, plan.Forward.Args, wantFwd)
			}
			if plan.Inverse == nil {
				t.Fatal("wifi_set_power is reversible: Inverse must not be nil")
			}
			wantInv := []string{"-setairportpower", "en0", tc.wantInverse}
			if plan.Inverse.Binary != "networksetup" || !equalStrings(plan.Inverse.Args, wantInv) {
				t.Errorf("inverse = %s %q, want networksetup %q", plan.Inverse.Binary, plan.Inverse.Args, wantInv)
			}
			// The preview must name the current state and the restore target so a
			// human approving the change knows exactly what undo will do.
			if !strings.Contains(plan.Preview, tc.prior) {
				t.Errorf("preview should mention prior state %q, got %q", tc.prior, plan.Preview)
			}
		})
	}
}

// TestStageWifiSetPower_RejectsUnknownState defends the enum switch: an
// out-of-enum value (only reachable in direct unit tests, since the registry
// validates the enum) is rejected before any networksetup probe runs.
func TestStageWifiSetPower_RejectsUnknownState(t *testing.T) {
	if _, err := stageWifiSetPower(context.Background(), lookupCapability(t, "wifi_set_power"), map[string]any{"power": "toggle"}); err == nil {
		t.Error("an unknown power state should be rejected")
	}
}

// TestParseWifiPower covers the on/off parse and the refuse-on-unrecognised
// contract that keeps staging from baking a wrong inverse.
func TestParseWifiPower(t *testing.T) {
	cases := []struct {
		in     string
		wantOn bool
		wantOK bool
	}{
		{"Wi-Fi Power (en0): On", true, true},
		{"Wi-Fi Power (en0): Off", false, true},
		{"  Wi-Fi Power (en1): On  \n", true, true},
		{"Wi-Fi Power (en0): Unknown", false, false},
		{"", false, false},
		{"garbage", false, false},
	}
	for _, tc := range cases {
		on, ok := parseWifiPower(tc.in)
		if on != tc.wantOn || ok != tc.wantOK {
			t.Errorf("parseWifiPower(%q) = (%v,%v), want (%v,%v)", tc.in, on, ok, tc.wantOn, tc.wantOK)
		}
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

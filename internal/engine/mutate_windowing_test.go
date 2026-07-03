// mutate_windowing_test.go covers the window mutators' pure, side-effect-free
// pieces: target/dimension validation, geometry parsing, the plan builders'
// forward/inverse/preview assembly (baking a prior state into the inverse), the
// osascript "--" injection guard, and the two-way permission-error mapping.
package engine

import (
	"strings"
	"testing"
)

func TestParseWindowTarget(t *testing.T) {
	// Default index when window_index is omitted.
	got, err := parseWindowTarget("move_window", map[string]any{"app": "Safari"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.app != "Safari" || got.index != defaultWindowIndex {
		t.Errorf("got %+v, want {Safari %d}", got, defaultWindowIndex)
	}

	// Explicit index is honoured.
	got, err = parseWindowTarget("move_window", map[string]any{"app": "Notes", "window_index": 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.index != 3 {
		t.Errorf("index = %d, want 3", got.index)
	}
}

func TestParseWindowTarget_Rejects(t *testing.T) {
	cases := map[string]map[string]any{
		"missing app":      {},
		"empty app":        {"app": "   "},
		"dash-leading app": {"app": "-e"},
		"control-char app": {"app": "bad\x00name"},
		"zero index":       {"app": "Safari", "window_index": 0},
		"negative index":   {"app": "Safari", "window_index": -2},
	}
	for name, in := range cases {
		if _, err := parseWindowTarget("move_window", in); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestPositiveDimension(t *testing.T) {
	if _, err := positiveDimension("resize_window", "width", "pixel", map[string]any{}); err == nil {
		t.Error("missing width: expected an error")
	}
	if _, err := positiveDimension("resize_window", "width", "pixel", map[string]any{"width": 0}); err == nil {
		t.Error("zero width: expected an error")
	}
	if _, err := positiveDimension("resize_window", "width", "pixel", map[string]any{"width": -10}); err == nil {
		t.Error("negative width: expected an error")
	}
	if _, err := positiveDimension("resize_window", "width", "pixel", map[string]any{"width": maxWindowDimension + 1}); err == nil {
		t.Error("oversize width: expected an error")
	}
	v, err := positiveDimension("resize_window", "width", "pixel", map[string]any{"width": 1024})
	if err != nil || v != 1024 {
		t.Errorf("valid width: got (%d, %v), want (1024, nil)", v, err)
	}
	// The unit word is caller-supplied and appears in the error text.
	if _, err := positiveDimension("capture_region", "height", "point", map[string]any{"height": 0}); err == nil || !strings.Contains(err.Error(), "point") {
		t.Errorf("expected a 'point'-worded error for capture_region, got: %v", err)
	}
}

func TestParseGeometry(t *testing.T) {
	x, y, w, h, err := parseGeometry("move_window", "100,80,1200,800\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if x != 100 || y != 80 || w != 1200 || h != 800 {
		t.Errorf("got (%d,%d,%d,%d), want (100,80,1200,800)", x, y, w, h)
	}

	// Negative coordinates (a display left of / above the main one) are valid.
	if x, y, _, _, err := parseGeometry("move_window", "-1440,-200,800,600"); err != nil || x != -1440 || y != -200 {
		t.Errorf("negative coords: got (%d,%d, err=%v)", x, y, err)
	}

	for _, bad := range []string{"", "1,2,3", "1,2,3,4,5", "a,2,3,4", "1,2,3,x"} {
		if _, _, _, _, err := parseGeometry("move_window", bad); err == nil {
			t.Errorf("parseGeometry(%q): expected an error", bad)
		}
	}
}

func TestPlanMoveWindow_ForwardInverseAndPreview(t *testing.T) {
	tgt := windowTarget{app: "Safari", index: 1}
	plan := planMoveWindow(tgt, 300, 200, 100, 80)

	// Forward carries the requested position after the "--" terminator.
	assertOsascriptArgs(t, "move forward", plan.Forward, setWindowPositionScript, "Safari", "1", "300", "200")
	// Inverse restores the PRIOR position observed at stage time.
	if plan.Inverse == nil {
		t.Fatal("move: expected an inverse")
	}
	assertOsascriptArgs(t, "move inverse", *plan.Inverse, setWindowPositionScript, "Safari", "1", "100", "80")

	if !strings.Contains(plan.Preview, "move it back to (100, 80)") {
		t.Errorf("preview missing undo target: %q", plan.Preview)
	}
}

func TestPlanResizeWindow_ForwardInverseAndPreview(t *testing.T) {
	tgt := windowTarget{app: "Notes", index: 2}
	plan := planResizeWindow(tgt, 1024, 768, 640, 480)

	assertOsascriptArgs(t, "resize forward", plan.Forward, setWindowSizeScript, "Notes", "2", "1024", "768")
	if plan.Inverse == nil {
		t.Fatal("resize: expected an inverse")
	}
	assertOsascriptArgs(t, "resize inverse", *plan.Inverse, setWindowSizeScript, "Notes", "2", "640", "480")

	if !strings.Contains(plan.Preview, "window 2") {
		t.Errorf("preview missing window label: %q", plan.Preview)
	}
	if !strings.Contains(plan.Preview, "restore 640×480") {
		t.Errorf("preview missing undo dimensions: %q", plan.Preview)
	}
}

func TestPlanMinimizeWindow_Undo(t *testing.T) {
	tgt := windowTarget{app: "Safari", index: 1}

	// Not already minimized -> forward minimizes, inverse un-minimizes.
	plan := planMinimizeWindow(tgt, false)
	assertOsascriptArgs(t, "minimize forward", plan.Forward, setWindowMinimizedScript, "Safari", "1", "true")
	if plan.Inverse == nil {
		t.Fatal("minimize (not-yet-minimized): expected an inverse")
	}
	assertOsascriptArgs(t, "minimize inverse", *plan.Inverse, setWindowMinimizedScript, "Safari", "1", "false")

	// Already minimized -> no undo offered (mirrors open_application on a running app).
	plan = planMinimizeWindow(tgt, true)
	if plan.Inverse != nil {
		t.Error("minimize (already minimized): expected no inverse")
	}
	if !strings.Contains(plan.Preview, "already appears minimized") {
		t.Errorf("preview should note it was already minimized: %q", plan.Preview)
	}
}

// TestWindowMutators_HostileAppLandsAsData is the osascript injection regression
// for the window mutators: a flag-like app name is emitted after the "--"
// terminator in every window script's argv, so it can never be read as an
// osascript option even though it flows through as the process name.
func TestWindowMutators_HostileAppLandsAsData(t *testing.T) {
	for _, h := range hostileValues {
		tgt := windowTarget{app: h, index: 1}
		// Every window mutator's forward command must place the (hostile) app name
		// after the "--" terminator as inert data.
		forwards := map[string]Command{
			"move":     planMoveWindow(tgt, 0, 0, 0, 0).Forward,
			"resize":   planResizeWindow(tgt, 1, 1, 1, 1).Forward,
			"minimize": planMinimizeWindow(tgt, false).Forward,
		}
		for op, cmd := range forwards {
			if got := cmd.Args; len(got) < 4 || got[2] != "--" || got[3] != h {
				t.Errorf("%s: app %q not immediately after '--' terminator: %q", op, h, got)
			}
		}
	}
}

func TestWindowScriptError_DistinguishesPermissions(t *testing.T) {
	// Accessibility denial ("assistive access") points at the Accessibility pane.
	err := windowScriptError("move_window", "System Events got an error: osascript is not allowed assistive access. (-25211)")
	if err == nil || !strings.Contains(err.Error(), "Accessibility") {
		t.Errorf("assistive-access error should mention Accessibility, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "Automation") && !strings.Contains(err.Error(), "separate from the Automation") {
		t.Errorf("assistive-access error should not be the Automation hint, got %v", err)
	}

	// Any other denial falls back to the Automation-focused System Events hint.
	err = windowScriptError("move_window", "Not authorized to send Apple events to System Events. (-1743)")
	if err == nil || !strings.Contains(err.Error(), "Automation") {
		t.Errorf("generic error should fall back to the Automation hint, got %v", err)
	}
}

func TestWindowLabel(t *testing.T) {
	if got := windowLabel(1); got != "front window" {
		t.Errorf("windowLabel(1) = %q, want front window", got)
	}
	if got := windowLabel(4); got != "window 4" {
		t.Errorf("windowLabel(4) = %q, want window 4", got)
	}
}

// assertOsascriptArgs checks that a Command is an osascript invocation running the
// given script with exactly the given data arguments after the "--" terminator.
func assertOsascriptArgs(t *testing.T, label string, cmd Command, script string, data ...string) {
	t.Helper()
	if cmd.Binary != "osascript" {
		t.Errorf("%s: binary = %q, want osascript", label, cmd.Binary)
		return
	}
	want := append([]string{"-e", script, "--"}, data...)
	if len(cmd.Args) != len(want) {
		t.Errorf("%s: argv = %q, want %q", label, cmd.Args, want)
		return
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Errorf("%s: argv[%d] = %q, want %q (full %q)", label, i, cmd.Args[i], want[i], cmd.Args)
		}
	}
}

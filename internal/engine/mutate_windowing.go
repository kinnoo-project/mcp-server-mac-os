// mutate_windowing.go implements the mutating window-management operations —
// move_window, resize_window, and minimize_window — that reposition, resize, or
// minimize a foreground app's window through the macOS Accessibility API (driven
// via System Events / osascript).
//
// # Lane
//
// All three are AUTO-COMMIT and reversible/low: moving, resizing, or minimizing a
// window is a benign, everyday action, so it runs immediately instead of waiting
// behind the execute token, and an undo is offered that restores exactly the
// state staging observed. Each mutator PROBES the window's prior state at stage
// time (its position, its size, or whether it was already minimized) and bakes
// that value into the inverse, so — per the engine's stage/commit contract — the
// undo replays a fixed command and recomputes nothing.
//
// # Addressing and safety
//
// A window is identified by its owning process name plus a 1-based front-to-back
// index (1 = frontmost), matching list_windows (builtins_windowing.go). The process
// name reaches every script as DATA bound to `on run argv` after the "--"
// terminator (applescript.go), and the index/coordinates are validated ints
// rendered to argv strings, so neither shell nor osascript option injection is
// possible. validateAppNameValue adds the same non-empty / no-leading-dash /
// no-control-character sanity check the other application mutators apply.
//
// # Permissions
//
// Unlike the launch/focus/quit mutators (which need only Automation access), the
// window operations reach into another app's UI through the Accessibility API, so
// they additionally require Accessibility ("assistive access") to be granted.
// windowScriptError distinguishes the two denials and points at the exact Settings
// pane for each.
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// defaultWindowIndex is the window targeted when the caller omits window_index:
// the frontmost window of the app, which is what "the window" means in almost
// every request ("move the Safari window").
const defaultWindowIndex = 1

// maxWindowDimension is a sane upper bound on a requested width/height. It is not
// a display measurement — AppleScript itself clamps an oversized window to the
// screen — but it rejects absurd or overflow-prone values (e.g. a million-pixel
// side) before they ever reach the script, keeping the input obviously bounded.
const maxWindowDimension = 100000

// setWindowPositionScript moves a window's top-left corner. argv:
// [process, index, x, y]; the index and coordinates are coerced back to integers
// on the AppleScript side (they were validated as ints in Go before rendering).
const setWindowPositionScript = `on run argv
	tell application "System Events"
		tell process (item 1 of argv)
			set position of window ((item 2 of argv) as integer) to {(item 3 of argv) as integer, (item 4 of argv) as integer}
		end tell
	end tell
end run`

// setWindowSizeScript resizes a window. argv: [process, index, width, height].
const setWindowSizeScript = `on run argv
	tell application "System Events"
		tell process (item 1 of argv)
			set size of window ((item 2 of argv) as integer) to {(item 3 of argv) as integer, (item 4 of argv) as integer}
		end tell
	end tell
end run`

// setWindowMinimizedScript sets a window's minimized state via the AXMinimized
// accessibility attribute (the reliable cross-app route; a plain `miniaturized`
// property is not exposed by every app). argv: [process, index, "true"|"false"].
const setWindowMinimizedScript = `on run argv
	tell application "System Events"
		tell process (item 1 of argv)
			set value of attribute "AXMinimized" of window ((item 2 of argv) as integer) to ((item 3 of argv) is "true")
		end tell
	end tell
end run`

// probeWindowGeometryScript returns a window's current position and size as
// "x,y,width,height" so a move/resize can bake the prior geometry into its
// inverse. argv: [process, index].
const probeWindowGeometryScript = `on run argv
	tell application "System Events"
		tell process (item 1 of argv)
			set w to window ((item 2 of argv) as integer)
			set p to position of w
			set s to size of w
			return ((item 1 of p) as string) & "," & ((item 2 of p) as string) & "," & ((item 1 of s) as string) & "," & ((item 2 of s) as string)
		end tell
	end tell
end run`

// probeWindowMinimizedScript returns "true"/"false" for a window's current
// AXMinimized state so minimize_window can bake the prior state into its inverse.
// argv: [process, index].
const probeWindowMinimizedScript = `on run argv
	tell application "System Events"
		tell process (item 1 of argv)
			return (value of attribute "AXMinimized" of window ((item 2 of argv) as integer)) as string
		end tell
	end tell
end run`

// windowTarget is the validated identity of the window a mutator will act on:
// the owning process name and its 1-based front-to-back index. Extracting the
// shared parse/validate step keeps every window mutator's guard identical.
type windowTarget struct {
	app   string
	index int
}

// parseWindowTarget extracts and validates the app name and optional window_index
// shared by every window mutator. The app name goes through validateAppNameValue
// (non-empty, no leading dash, no control characters); the index defaults to the
// front window and must be a positive integer (a 0 or negative index is not a
// real window address and would surface as a confusing AppleScript error later).
func parseWindowTarget(op string, in map[string]any) (windowTarget, error) {
	appRaw, _ := getString(in, "app")
	app, err := validateAppNameValue(op, "app", appRaw)
	if err != nil {
		return windowTarget{}, err
	}
	index := defaultWindowIndex
	if v, ok := getInt(in, "window_index"); ok {
		index = v
	}
	if index < 1 {
		return windowTarget{}, fmt.Errorf("%s: window_index must be 1 or greater (1 = frontmost), got %d", op, index)
	}
	return windowTarget{app: app, index: index}, nil
}

// probeWindowGeometry reads a window's current top-left position and size via
// System Events so a move/resize can construct an inverse that restores exactly
// what was there. A denied permission (Automation or Accessibility) or a missing
// window surfaces as an actionable windowScriptError rather than a bare
// AppleScript error.
func probeWindowGeometry(ctx context.Context, op string, t windowTarget) (x, y, w, h int, err error) {
	res, err := runOsascript(ctx, probeWindowGeometryScript, t.app, itoa(t.index))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if res.ExitCode != 0 {
		return 0, 0, 0, 0, windowScriptError(op, res.Stderr)
	}
	return parseGeometry(op, res.Stdout)
}

// parseGeometry parses the "x,y,width,height" line probeWindowGeometryScript
// emits into four integers. It is pure so the parsing (and its error handling)
// is unit-testable without a live probe.
func parseGeometry(op, stdout string) (x, y, w, h int, err error) {
	parts := strings.Split(strings.TrimSpace(stdout), ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("%s: could not read the window's geometry (unexpected probe output %q)", op, strings.TrimSpace(stdout))
	}
	vals := make([]int, 4)
	for i, p := range parts {
		n, convErr := strconv.Atoi(strings.TrimSpace(p))
		if convErr != nil {
			return 0, 0, 0, 0, fmt.Errorf("%s: could not read the window's geometry (non-integer field %q)", op, strings.TrimSpace(p))
		}
		vals[i] = n
	}
	return vals[0], vals[1], vals[2], vals[3], nil
}

// stageMoveWindow stages (for immediate auto-commit) moving a window's top-left
// corner to (x, y). It probes the window's current position and bakes it into the
// inverse so undo returns the window to exactly where it started. x and y may be
// negative — a legitimate coordinate on a display arranged left of or above the
// main one — so they are NOT range-checked; AppleScript clamps a window dragged
// off-screen.
func stageMoveWindow(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	t, err := parseWindowTarget("move_window", in)
	if err != nil {
		return nil, err
	}
	x, ok := getInt(in, "x")
	if !ok {
		return nil, fmt.Errorf("move_window: 'x' is required")
	}
	y, ok := getInt(in, "y")
	if !ok {
		return nil, fmt.Errorf("move_window: 'y' is required")
	}

	priorX, priorY, _, _, err := probeWindowGeometry(ctx, "move_window", t)
	if err != nil {
		return nil, err
	}
	return planMoveWindow(t, x, y, priorX, priorY), nil
}

// planMoveWindow is the pure half of stageMoveWindow: given the target, the
// requested position, and the prior position observed at stage time, it assembles
// the forward/inverse commands and preview with no side effects. Split out so the
// argv layout, "--" terminator, and preview text are unit-testable without a live
// (permission-gated) System Events probe (mirroring planSetAppearance).
func planMoveWindow(t windowTarget, x, y, priorX, priorY int) *StagedPlan {
	inverse := osascriptCommand(setWindowPositionScript, t.app, itoa(t.index), itoa(priorX), itoa(priorY))
	return &StagedPlan{
		Preview: fmt.Sprintf("Move %s's %s to (%d, %d) (currently at (%d, %d)). Undo will move it back to (%d, %d).",
			t.app, windowLabel(t.index), x, y, priorX, priorY, priorX, priorY),
		Forward: osascriptCommand(setWindowPositionScript, t.app, itoa(t.index), itoa(x), itoa(y)),
		Inverse: &inverse,
	}
}

// stageResizeWindow stages (for immediate auto-commit) resizing a window to
// width×height. It probes the window's current size and bakes it into the inverse
// so undo restores the prior dimensions. Width and height must be positive (a
// zero or negative size is not a real window) and are bounded by maxWindowDimension.
func stageResizeWindow(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	t, err := parseWindowTarget("resize_window", in)
	if err != nil {
		return nil, err
	}
	width, err := positiveDimension("resize_window", "width", in)
	if err != nil {
		return nil, err
	}
	height, err := positiveDimension("resize_window", "height", in)
	if err != nil {
		return nil, err
	}

	_, _, priorW, priorH, err := probeWindowGeometry(ctx, "resize_window", t)
	if err != nil {
		return nil, err
	}
	return planResizeWindow(t, width, height, priorW, priorH), nil
}

// planResizeWindow is the pure half of stageResizeWindow: given the target, the
// requested size, and the prior size observed at stage time, it assembles the
// forward/inverse commands and preview with no side effects.
func planResizeWindow(t windowTarget, width, height, priorW, priorH int) *StagedPlan {
	inverse := osascriptCommand(setWindowSizeScript, t.app, itoa(t.index), itoa(priorW), itoa(priorH))
	return &StagedPlan{
		Preview: fmt.Sprintf("Resize %s's %s to %d×%d pixels (currently %d×%d). Undo will restore %d×%d.",
			t.app, windowLabel(t.index), width, height, priorW, priorH, priorW, priorH),
		Forward: osascriptCommand(setWindowSizeScript, t.app, itoa(t.index), itoa(width), itoa(height)),
		Inverse: &inverse,
	}
}

// stageMinimizeWindow stages (for immediate auto-commit) minimizing a window to
// the Dock. It probes whether the window is ALREADY minimized: if it is, no
// inverse is offered (an undo that un-minimized would be surprising given the
// window was already down), mirroring how open_application declines an undo when
// the app was already running. Otherwise the inverse un-minimizes it.
func stageMinimizeWindow(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	t, err := parseWindowTarget("minimize_window", in)
	if err != nil {
		return nil, err
	}

	priorMinimized, err := probeWindowMinimized(ctx, "minimize_window", t)
	if err != nil {
		return nil, err
	}
	return planMinimizeWindow(t, priorMinimized), nil
}

// planMinimizeWindow is the pure half of stageMinimizeWindow: given the target
// and whether the window was already minimized at stage time, it assembles the
// forward command and (unless already minimized) the un-minimize inverse.
func planMinimizeWindow(t windowTarget, priorMinimized bool) *StagedPlan {
	forward := osascriptCommand(setWindowMinimizedScript, t.app, itoa(t.index), "true")
	if priorMinimized {
		return &StagedPlan{
			Preview: fmt.Sprintf("Minimize %s's %s (it already appears minimized, so this has no visible effect and offers no undo).", t.app, windowLabel(t.index)),
			Forward: forward,
			Inverse: nil,
		}
	}
	inverse := osascriptCommand(setWindowMinimizedScript, t.app, itoa(t.index), "false")
	return &StagedPlan{
		Preview: fmt.Sprintf("Minimize %s's %s to the Dock. Undo will un-minimize it.", t.app, windowLabel(t.index)),
		Forward: forward,
		Inverse: &inverse,
	}
}

// probeWindowMinimized reads a window's current AXMinimized state via System
// Events so minimize_window can decide whether to offer an un-minimize undo.
func probeWindowMinimized(ctx context.Context, op string, t windowTarget) (bool, error) {
	res, err := runOsascript(ctx, probeWindowMinimizedScript, t.app, itoa(t.index))
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, windowScriptError(op, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout) == "true", nil
}

// positiveDimension reads a required int dimension (width/height) and enforces
// that it is a positive, sanely-bounded pixel count.
func positiveDimension(op, field string, in map[string]any) (int, error) {
	v, ok := getInt(in, field)
	if !ok {
		return 0, fmt.Errorf("%s: '%s' is required", op, field)
	}
	if v < 1 {
		return 0, fmt.Errorf("%s: %s must be at least 1 pixel, got %d", op, field, v)
	}
	if v > maxWindowDimension {
		return 0, fmt.Errorf("%s: %s of %d exceeds the %d-pixel limit", op, field, v, maxWindowDimension)
	}
	return v, nil
}

// windowLabel renders a 1-based window index as the user-facing phrase used in
// previews ("front window" for index 1, "window 2" otherwise).
func windowLabel(index int) string {
	if index == defaultWindowIndex {
		return "front window"
	}
	return fmt.Sprintf("window %d", index)
}

// windowScriptError surfaces the most likely cause of a failed window script.
// Window control needs TWO distinct grants, so the two denials are distinguished:
// an "assistive access" / -25211 error means Accessibility has not been granted
// (a separate Settings pane from Automation), while any other non-zero exit is
// most likely the Automation grant for System Events — for which the shared
// systemEventsScriptError helper (mutate_preferences.go, added by set_appearance)
// already produces the right hint.
func windowScriptError(op, stderr string) error {
	s := strings.TrimSpace(stderr)
	low := strings.ToLower(s)
	if strings.Contains(low, "assistive access") || strings.Contains(low, "-25211") {
		if s == "" {
			s = "osascript reported a non-zero exit with no detail"
		}
		return fmt.Errorf("%s: controlling windows failed (%s). macOS needs you to grant this app Accessibility access in System Settings → Privacy & Security → Accessibility (this is separate from the Automation grant)", op, s)
	}
	return systemEventsScriptError(op, stderr)
}

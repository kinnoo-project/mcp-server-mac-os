// builtins_screenshot_region.go implements the two cropped screen-capture
// builtins that sit alongside capture_screen (builtins_screenshot.go):
//
//   - capture_region — photograph an explicit rectangle (x, y, width, height) of
//     the screen, given in global screen points.
//   - capture_window — photograph a specific app window. AppleScript cannot hand
//     screencapture the CGWindowID its native -l window mode needs, so instead we
//     RESOLVE the window's on-screen bounds through the same System Events read
//     the window mutators use (probeWindowGeometry, mutate_windowing.go) and then
//     capture that rectangle with -R. The visible result is identical for a
//     normal, unobscured window.
//
// Both funnel into runCapture (builtins_screenshot.go): they only differ in how
// the -R rectangle is derived, so the output-path guarding, no-overwrite
// create-only contract, and Screen-Recording-denial handling are all shared and
// tested once. Both are read-only/low — a capture never mutates system state.
//
// Permissions: capture_region needs only Screen Recording. capture_window needs
// Screen Recording PLUS the Automation + Accessibility grants that reading a
// window's geometry through System Events requires; a missing window-read grant
// surfaces via windowScriptError before any capture is attempted.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/registry"
)

// runCaptureRegion captures an explicit rectangle of the screen. The four
// coordinates are validated ints (the normalizer guarantees whole numbers): x
// and y may be negative — a legitimate coordinate on a display arranged left of
// or above the main one — while width and height must be positive and sanely
// bounded, since a zero/negative or absurd size is not a real capture rectangle.
func runCaptureRegion(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	x, ok := getInt(in, "x")
	if !ok {
		return "", fmt.Errorf("capture_region: 'x' is required")
	}
	y, ok := getInt(in, "y")
	if !ok {
		return "", fmt.Errorf("capture_region: 'y' is required")
	}
	// width/height reuse positiveDimension (mutate_windowing.go): required,
	// at least 1 point, and bounded by maxWindowDimension against overflow-prone
	// or absurd values. The "point" unit word matches the manifest's vocabulary
	// (screencapture geometry is in screen points, not raw pixels).
	width, err := positiveDimension("capture_region", "width", "point", in)
	if err != nil {
		return "", err
	}
	height, err := positiveDimension("capture_region", "height", "point", in)
	if err != nil {
		return "", err
	}

	// The rectangle is fully known already, so regionFn just hands it back; there
	// is no permission-gated work to defer for capture_region.
	region := &captureRegion{x: x, y: y, w: width, h: height}
	return runCapture(ctx, captureRequest{
		op: "capture_region", in: in,
		regionFn: func() (*captureRegion, error) { return region, nil },
	})
}

// runCaptureWindow captures a specific app window by first reading its current
// on-screen bounds through System Events, then capturing that rectangle. The app
// name and optional 1-based window_index are validated by parseWindowTarget (the
// same guard the window mutators use: non-empty, no leading dash, no control
// characters), and the app name reaches the geometry-probe script only as inert
// argv data after the "--" terminator — so neither shell nor osascript option
// injection is possible.
//
// A zero-size window (width or height reported as 0, which a fully minimized or
// off-screen window can be) is rejected: screencapture -R with a zero dimension
// would silently produce an empty image that our create-only spine would then
// treat as a permission failure, so we give a clearer error instead.
//
// The window-bounds read is deferred into regionFn so runCapture invokes it only
// AFTER validating format/output_path. That ordering matters because the probe is
// permission-gated: a request with a dash-leading output_path is rejected before
// the probe runs, so it never provokes an Automation/Accessibility prompt for a
// request that was going to fail on its path anyway.
func runCaptureWindow(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	t, err := parseWindowTarget("capture_window", in)
	if err != nil {
		return "", err
	}

	regionFn := func() (*captureRegion, error) {
		x, y, w, h, err := probeWindowGeometry(ctx, "capture_window", t)
		if err != nil {
			return nil, err
		}
		if w < 1 || h < 1 {
			return nil, fmt.Errorf("capture_window: %s's %s reports a zero-size rectangle (%d×%d); it may be minimized or off-screen, so there is nothing to capture", t.app, windowLabel(t.index), w, h)
		}
		return &captureRegion{x: x, y: y, w: w, h: h}, nil
	}
	return runCapture(ctx, captureRequest{op: "capture_window", in: in, regionFn: regionFn})
}

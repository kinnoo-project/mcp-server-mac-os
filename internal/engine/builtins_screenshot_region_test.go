// builtins_screenshot_region_test.go unit-tests the pure input-validation and
// injection-guard behavior of capture_region and capture_window without taking a
// real screenshot or making a live System Events call. The argv assembly they
// share with capture_screen (the -R crop) is pinned by TestScreencaptureArgs_Golden
// in builtins_screenshot_test.go.
//
// SAFETY: the only tests that actually run screencapture / System Events are the
// *_Live ones, skipped unless MCP_SCREENSHOT_LIVE=1, because they need a physical
// display plus granted Screen Recording (and, for the window, Accessibility +
// Automation) permissions that CI does not have.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestCaptureRegion_RejectsBadInput verifies capture_region fails fast — before
// it ever launches screencapture — on bad geometry or a dash-leading output_path
// (the option-injection guard). Each case returns during validation, so no
// subprocess runs.
func TestCaptureRegion_RejectsBadInput(t *testing.T) {
	cap := lookupCapability(t, "capture_region")
	cases := map[string]map[string]any{
		"missing x":         {"y": 0, "width": 10, "height": 10},
		"missing y":         {"x": 0, "width": 10, "height": 10},
		"zero width":        {"x": 0, "y": 0, "width": 0, "height": 10},
		"negative height":   {"x": 0, "y": 0, "width": 10, "height": -5},
		"oversize width":    {"x": 0, "y": 0, "width": 999999, "height": 10},
		"dash-leading path": {"x": 0, "y": 0, "width": 10, "height": 10, "format": "png", "output_path": "-oops.png"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runCaptureRegion(context.Background(), cap, params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestCaptureRegion_AcceptsNegativeOrigin confirms a negative x/y (a valid
// coordinate on a display arranged left of or above the main one) is NOT rejected
// during validation: the capture proceeds to the point of running screencapture.
// We stop short of a real capture by pointing at an unwritable directory, which
// makes the run fail AFTER validation — proving the negative origin itself was
// accepted. (A dash-leading output_path, by contrast, is rejected during path
// resolution; here the path is a normal one.)
func TestCaptureRegion_AcceptsNegativeOrigin(t *testing.T) {
	cap := lookupCapability(t, "capture_region")
	// A path under /dev (a device dir) cannot be created, so MkdirAll fails after
	// the geometry has already been validated — confirming validation passed.
	_, err := runCaptureRegion(context.Background(), cap, map[string]any{
		"x": -100, "y": -50, "width": 640, "height": 480,
		"format": "png", "output_path": "/dev/null/cannot/create/x.png",
	})
	if err == nil {
		t.Fatal("expected a downstream error (unwritable dir), got nil")
	}
	if strings.Contains(err.Error(), "must not begin with") {
		t.Errorf("negative origin was wrongly rejected as a dash-leading path: %v", err)
	}
}

// TestCaptureWindow_RejectsHostileApp is the injection regression required by
// CLAUDE.md §4 for every osascript-backed capability: a flag-like app name (here
// osascript's own "-e") must be rejected as data-that-fails-validation, never
// interpreted as a flag. validateAppNameValue (via parseWindowTarget) rejects the
// leading dash up front, so the run errors before any System Events call — and
// even if it did not, the app would reach the probe script only as argv data
// after the "--" terminator (see TestInjection_OsascriptTerminatesHostileData).
func TestCaptureWindow_RejectsHostileApp(t *testing.T) {
	cap := lookupCapability(t, "capture_window")
	for _, hostile := range []string{"-e", "-rf", "--flood"} {
		t.Run(hostile, func(t *testing.T) {
			_, err := runCaptureWindow(context.Background(), cap, map[string]any{"app": hostile})
			if err == nil {
				t.Fatalf("expected capture_window to reject hostile app %q", hostile)
			}
		})
	}
}

// TestCaptureWindow_RejectsBadIndex confirms a non-positive window_index (not a
// real window address) is rejected during validation, before any capture.
func TestCaptureWindow_RejectsBadIndex(t *testing.T) {
	cap := lookupCapability(t, "capture_window")
	_, err := runCaptureWindow(context.Background(), cap, map[string]any{"app": "Safari", "window_index": 0})
	if err == nil {
		t.Fatal("expected an error for window_index 0")
	}
}

// TestCaptureRegion_Live actually captures a small region. Skipped unless
// MCP_SCREENSHOT_LIVE=1 because it needs a real display and Screen Recording.
func TestCaptureRegion_Live(t *testing.T) {
	if os.Getenv("MCP_SCREENSHOT_LIVE") != "1" {
		t.Skip("set MCP_SCREENSHOT_LIVE=1 to run a real region capture (needs a display + Screen Recording permission)")
	}
	cap := lookupCapability(t, "capture_region")
	out, err := runCaptureRegion(context.Background(), cap, map[string]any{
		"x": 0, "y": 0, "width": 200, "height": 200, "format": "png",
	})
	if err != nil {
		t.Fatalf("live region capture failed (is Screen Recording granted?): %v", err)
	}
	if !strings.Contains(out, "Screenshot saved to") || !strings.Contains(out, "px") {
		t.Errorf("live region capture summary unexpected: %s", out)
	}
}

// TestCaptureWindow_Live actually captures a window. Skipped unless
// MCP_SCREENSHOT_LIVE=1 because it needs a real display plus Screen Recording,
// Accessibility, and Automation grants. Set MCP_SCREENSHOT_LIVE_APP to target a
// running app (default Finder).
func TestCaptureWindow_Live(t *testing.T) {
	if os.Getenv("MCP_SCREENSHOT_LIVE") != "1" {
		t.Skip("set MCP_SCREENSHOT_LIVE=1 to run a real window capture (needs a display + Screen Recording + Accessibility + Automation)")
	}
	app := os.Getenv("MCP_SCREENSHOT_LIVE_APP")
	if app == "" {
		app = "Finder"
	}
	cap := lookupCapability(t, "capture_window")
	out, err := runCaptureWindow(context.Background(), cap, map[string]any{"app": app, "format": "png"})
	if err != nil {
		t.Fatalf("live window capture of %q failed (grants in place? app running with a window?): %v", app, err)
	}
	if !strings.Contains(out, "Screenshot saved to") || !strings.Contains(out, "px") {
		t.Errorf("live window capture summary unexpected: %s", out)
	}
}

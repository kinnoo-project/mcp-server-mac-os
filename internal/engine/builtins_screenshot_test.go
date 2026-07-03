// builtins_screenshot_test.go unit-tests capture_screen's pure pieces — argv
// assembly, input validation, format mapping, dimension reading, and the
// permission-error message — without taking a real screenshot.
//
// SAFETY: the only test that actually runs screencapture is TestCaptureScreen_Live,
// which is skipped unless MCP_SCREENSHOT_LIVE=1, because a real capture needs a
// physical display and a granted Screen Recording permission that CI does not have.
package engine

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestScreencaptureArgs_Golden pins the exact flag ordering screencapture is
// invoked with, including the optional -D display flag and the path as the
// trailing operand.
func TestScreencaptureArgs_Golden(t *testing.T) {
	cases := []struct {
		name       string
		format     string
		display    int
		hasDisplay bool
		region     *captureRegion
		path       string
		want       []string
	}{
		{
			name:   "png main display no region",
			format: "png", hasDisplay: false, path: "/tmp/mcp-fallback/screenshots/a.png",
			want: []string{"-x", "-t", "png", "/tmp/mcp-fallback/screenshots/a.png"},
		},
		{
			name:   "jpg second display no region",
			format: "jpg", display: 2, hasDisplay: true, path: "/tmp/mcp-fallback/screenshots/b.jpg",
			want: []string{"-x", "-t", "jpg", "-D", "2", "/tmp/mcp-fallback/screenshots/b.jpg"},
		},
		{
			name:   "png region crop",
			format: "png", region: &captureRegion{x: 10, y: 20, w: 300, h: 400}, path: "/tmp/mcp-fallback/screenshots/c.png",
			want: []string{"-x", "-t", "png", "-R", "10,20,300,400", "/tmp/mcp-fallback/screenshots/c.png"},
		},
		{
			name:   "region with negative origin",
			format: "png", region: &captureRegion{x: -100, y: -50, w: 640, h: 480}, path: "/tmp/mcp-fallback/screenshots/d.png",
			want: []string{"-x", "-t", "png", "-R", "-100,-50,640,480", "/tmp/mcp-fallback/screenshots/d.png"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := screencaptureArgs(tc.format, tc.display, tc.hasDisplay, tc.region, tc.path)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("screencaptureArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCaptureScreen_RejectsBadInput verifies capture_screen fails fast — before
// it ever launches screencapture — on bad input: an unsupported format, a
// non-positive display, a dash-leading output_path (the option-injection guard),
// and an output_path with an unsupported extension. These all return during
// validation/path-resolution, so no subprocess runs.
func TestCaptureScreen_RejectsBadInput(t *testing.T) {
	cap := lookupCapability(t, "capture_screen")
	cases := map[string]map[string]any{
		"bad format":        {"format": "bmp"},
		"zero display":      {"format": "png", "display": 0},
		"negative display":  {"format": "png", "display": -1},
		"dash-leading path": {"format": "png", "output_path": "-oops.png"},
		"unsupported ext":   {"format": "png", "output_path": "/tmp/x.webp"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runCaptureScreen(context.Background(), cap, params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestResolveScreenshotPath covers the output_path resolution rules: the default
// location, an existing directory (filename generated inside), a full file path
// whose extension drives the format (incl. the jpeg→jpg alias), a path with no
// extension (format's extension appended), and the unsupported-extension and
// dash-leading rejections.
func TestResolveScreenshotPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	dir := t.TempDir() // a real existing directory

	t.Run("default location", func(t *testing.T) {
		path, format, err := resolveScreenshotPath("capture_screen", "", "png")
		if err != nil {
			t.Fatal(err)
		}
		wantDir := filepath.Join(home, "Pictures", "Screenshots")
		if filepath.Dir(path) != wantDir || format != "png" || !strings.HasSuffix(path, ".png") {
			t.Errorf("default = %q (format %q), want a generated .png under %q", path, format, wantDir)
		}
	})

	t.Run("existing directory generates name inside", func(t *testing.T) {
		path, format, err := resolveScreenshotPath("capture_screen", dir, "jpg")
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(path) != dir || format != "jpg" || !strings.HasSuffix(path, ".jpg") {
			t.Errorf("dir target = %q (format %q), want generated .jpg under %q", path, format, dir)
		}
	})

	t.Run("full path extension wins over format", func(t *testing.T) {
		in := filepath.Join(dir, "shot.jpg")
		path, format, err := resolveScreenshotPath("capture_screen", in, "png") // png param, .jpg path
		if err != nil {
			t.Fatal(err)
		}
		if path != in || format != "jpg" {
			t.Errorf("got %q/%q, want %q/jpg (extension should win)", path, format, in)
		}
	})

	t.Run("jpeg alias maps to jpg", func(t *testing.T) {
		in := filepath.Join(dir, "shot.jpeg")
		path, format, err := resolveScreenshotPath("capture_screen", in, "png")
		if err != nil {
			t.Fatal(err)
		}
		if path != in || format != "jpg" {
			t.Errorf("got %q/%q, want %q/jpg", path, format, in)
		}
	})

	t.Run("no extension appends format extension", func(t *testing.T) {
		in := filepath.Join(dir, "shot")
		path, format, err := resolveScreenshotPath("capture_screen", in, "png")
		if err != nil {
			t.Fatal(err)
		}
		if path != in+".png" || format != "png" {
			t.Errorf("got %q/%q, want %q/png", path, format, in+".png")
		}
	})

	t.Run("unsupported extension rejected", func(t *testing.T) {
		if _, _, err := resolveScreenshotPath("capture_screen", filepath.Join(dir, "x.webp"), "png"); err == nil {
			t.Error("expected an error for an unsupported extension")
		}
	})

	t.Run("dash-leading rejected", func(t *testing.T) {
		if _, _, err := resolveScreenshotPath("capture_screen", "-x.png", "png"); err == nil {
			t.Error("expected an error for a dash-leading output_path")
		}
	})
}

// TestCaptureScreen_RefusesOverwrite verifies a capture aimed at an already
// existing file fails before screencapture runs, preserving the create-only (and
// thus read-only-safe) contract. The error path runs entirely on a temp file.
func TestCaptureScreen_RefusesOverwrite(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "taken.png")
	if err := os.WriteFile(existing, []byte("not empty"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	cap := lookupCapability(t, "capture_screen")
	_, err := runCaptureScreen(context.Background(), cap, map[string]any{"format": "png", "output_path": existing})
	if err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("expected an overwrite-refusal error, got %v", err)
	}
}

// TestScreenshotFormats_MatchManifestEnum guards the invariant that every format
// the manifest advertises has a matching entry in screenshotFormats (and vice
// versa), so an enum value can never reach runCaptureScreen with no mapping.
func TestScreenshotFormats_MatchManifestEnum(t *testing.T) {
	cap := lookupCapability(t, "capture_screen")
	var enum []string
	for _, p := range cap.Params {
		if p.Name == "format" {
			enum = p.Enum
		}
	}
	if len(enum) == 0 {
		t.Fatal("capture_screen manifest has no 'format' enum")
	}
	if len(enum) != len(screenshotFormats) {
		t.Errorf("manifest enum has %d formats, screenshotFormats has %d", len(enum), len(screenshotFormats))
	}
	for _, f := range enum {
		if _, ok := screenshotFormats[f]; !ok {
			t.Errorf("manifest format %q has no screenshotFormats entry", f)
		}
	}
}

// TestScreencapturePermissionError verifies the message always points at the
// Screen Recording grant, and folds in stderr detail when present or a synthetic
// exit-code note when stderr is empty (the common silent-denial case).
func TestScreencapturePermissionError(t *testing.T) {
	withStderr := screencapturePermissionError("capture_screen", 1, "could not create image").Error()
	if !strings.Contains(withStderr, "could not create image") || !strings.Contains(withStderr, "Screen Recording") {
		t.Errorf("error should carry stderr detail and the grant hint: %s", withStderr)
	}
	empty := screencapturePermissionError("capture_screen", 0, "").Error()
	if !strings.Contains(empty, "exit code 0") || !strings.Contains(empty, "Screen Recording") {
		t.Errorf("empty-stderr error should note the exit code and the grant hint: %s", empty)
	}
}

// TestImageDimensions reads dimensions back from a real PNG written to a temp
// file, and confirms a non-image file reports ok=false (the PDF/TIFF path, where
// the caller simply omits dimensions).
func TestImageDimensions(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "x.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 12, 7))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(pngPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if w, h, ok := imageDimensions(pngPath); !ok || w != 12 || h != 7 {
		t.Errorf("imageDimensions = %d,%d,%v; want 12,7,true", w, h, ok)
	}

	notImage := filepath.Join(dir, "x.pdf")
	if err := os.WriteFile(notImage, []byte("%PDF-1.4 not a real image"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if _, _, ok := imageDimensions(notImage); ok {
		t.Error("imageDimensions should report ok=false for a non-decodable file")
	}
}

// TestReportCapture checks the success summary contains the path and size, and
// omits dimensions for a non-decodable format without touching the filesystem.
func TestReportCapture(t *testing.T) {
	out := reportCapture("/tmp/mcp-fallback/screenshots/s.pdf", 2048, false)
	if !strings.Contains(out, "/tmp/mcp-fallback/screenshots/s.pdf") {
		t.Errorf("summary missing path: %s", out)
	}
	if !strings.Contains(out, "2.0K") {
		t.Errorf("summary missing human-readable size: %s", out)
	}
	if strings.Contains(out, "px") {
		t.Errorf("non-decodable format should not report dimensions: %s", out)
	}
}

// TestCaptureScreen_Live actually captures the screen. It is skipped unless
// MCP_SCREENSHOT_LIVE=1 because it requires a real display and a granted Screen
// Recording permission. When run, it asserts a non-empty file is reported.
func TestCaptureScreen_Live(t *testing.T) {
	if os.Getenv("MCP_SCREENSHOT_LIVE") != "1" {
		t.Skip("set MCP_SCREENSHOT_LIVE=1 to run a real screen capture (needs a display + Screen Recording permission)")
	}
	cap := lookupCapability(t, "capture_screen")
	out, err := runCaptureScreen(context.Background(), cap, map[string]any{"format": "png"})
	if err != nil {
		t.Fatalf("live capture failed (is Screen Recording granted?): %v", err)
	}
	if !strings.Contains(out, "Screenshot saved to") || !strings.Contains(out, "px") {
		t.Errorf("live capture summary unexpected: %s", out)
	}
}

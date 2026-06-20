// builtins_screenshot.go implements the read-only screen-capture builtin:
// capture_screen, which photographs the desktop with macOS's screencapture(1)
// and reports where the image was saved plus its dimensions and size.
//
// It is a "builtin" (run in-process, like list_printers) rather than a plain
// argv-builder for one reason: screencapture frequently exits 0 even when the
// Screen Recording permission is denied — it simply writes an empty or
// desktop-only file. Only by running the tool AND THEN inspecting the resulting
// file can we turn that silent failure into an actionable "grant Screen
// Recording" message. A pure argv-builder never sees the output, so it could not.
//
// Output-path policy: the destination is ALWAYS server-generated inside a scratch
// directory we own; the model never chooses it. That removes the dash-leading
// path-injection surface entirely (the path is an absolute string we built) and
// keeps every captured image an artifact the server can account for.
package engine

import (
	"context"
	"fmt"
	"image"
	// Register the PNG and JPEG decoders so image.DecodeConfig can read the
	// dimensions of the formats we can introspect. PDF/TIFF have no stdlib
	// decoder, so their dimensions are simply omitted (see reportCapture).
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// screenshotDir is the server-owned scratch directory every capture is written
// to. It sits under the project's established /tmp/mcp-fallback convention (see
// mutate_printers.go) so captured images are easy to find and clean up.
const screenshotDir = "/tmp/mcp-fallback/screenshots"

// screenshotFormat describes one supported output format: the screencapture -t
// token, the file extension, and whether stdlib can read its pixel dimensions.
type screenshotFormat struct {
	ext       string // file extension, e.g. "png"
	decodable bool   // true when image.DecodeConfig can read its dimensions
}

// screenshotFormats maps each manifest enum value to its format details. The
// manifest enum and this map MUST stay in sync; an unknown format is rejected
// defensively in runCaptureScreen even though the registry already validates it.
var screenshotFormats = map[string]screenshotFormat{
	"png":  {ext: "png", decodable: true},
	"jpg":  {ext: "jpg", decodable: true},
	"pdf":  {ext: "pdf", decodable: false},
	"tiff": {ext: "tiff", decodable: false},
}

// runCaptureScreen captures the screen and returns a human-readable summary of
// where the image landed and its dimensions/size. A denied Screen Recording
// permission (which screencapture reports as an empty/zero-byte file, often with
// exit 0) is surfaced as an actionable grant message.
func runCaptureScreen(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	format, _ := getString(in, "format") // enum-validated by the normalizer; defaults to "png"
	spec, ok := screenshotFormats[format]
	if !ok {
		return "", fmt.Errorf("capture_screen: unsupported format %q", format)
	}

	// display is optional. When present it must be a positive 1-based index;
	// reject zero/negative up front rather than handing screencapture a value it
	// would treat as "no such display" (a confusing empty capture).
	display, hasDisplay := getInt(in, "display")
	if hasDisplay && display < 1 {
		return "", fmt.Errorf("capture_screen: 'display' must be 1 or greater (1 is the main display), got %d", display)
	}

	bin, err := policy.ResolveBinary("screencapture")
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
		return "", fmt.Errorf("capture_screen: could not create output directory %s: %w", screenshotDir, err)
	}
	// Server-generated, collision-resistant absolute path: timestamp down to the
	// nanosecond so rapid successive captures never overwrite one another.
	outPath := filepath.Join(screenshotDir, fmt.Sprintf("screen-%s.%s", time.Now().Format("20060102-150405.000000000"), spec.ext))

	args := screencaptureArgs(format, display, hasDisplay, outPath)
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return "", err
	}

	// Treat a non-zero exit, a missing file, or a zero-byte file as a failed
	// capture — the dominant real-world cause is a denied Screen Recording
	// permission, which screencapture reports this way rather than with a clear
	// error. Remove a stray empty file so the scratch dir does not accumulate them.
	info, statErr := os.Stat(outPath)
	if res.ExitCode != 0 || statErr != nil || info.Size() == 0 {
		if statErr == nil && info.Size() == 0 {
			_ = os.Remove(outPath)
		}
		return "", screencapturePermissionError(res.ExitCode, res.Stderr)
	}

	return reportCapture(outPath, info.Size(), spec.decodable), nil
}

// screencaptureArgs assembles the screencapture argument vector. It is factored
// out as a pure function so the exact flag ordering can be unit-tested without
// running the binary. The output path is always the trailing operand and is a
// server-generated absolute path, so no "--" terminator or dash-guard is needed.
func screencaptureArgs(format string, display int, hasDisplay bool, outPath string) []string {
	// -x suppresses the capture sound (there is no human at the prompt).
	// -t selects the image format.
	args := []string{"-x", "-t", format}
	if hasDisplay {
		// -D selects which display to capture (1-based).
		args = append(args, "-D", fmt.Sprintf("%d", display))
	}
	return append(args, outPath)
}

// reportCapture renders the success summary: path, pixel dimensions (when the
// format is one stdlib can decode), human-readable size, and format.
func reportCapture(path string, size int64, decodable bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Screenshot saved to %s\n", path)
	if decodable {
		if w, h, ok := imageDimensions(path); ok {
			fmt.Fprintf(&b, "  %dx%d px\n", w, h)
		}
	}
	fmt.Fprintf(&b, "  %s", formatBytes(size))
	return b.String()
}

// imageDimensions reads just the header of an image file to report its width and
// height, without decoding the whole image. It returns ok=false when the file
// cannot be opened or its format has no registered decoder (PDF/TIFF), in which
// case the caller simply omits dimensions rather than failing the capture.
func imageDimensions(path string) (width, height int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// screencapturePermissionError turns a failed capture into an actionable message,
// mirroring messagesDBError/appScriptError. An empty/zero-byte image is almost
// always a denied Screen Recording grant, so we lead with that remedy.
func screencapturePermissionError(exitCode int, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	detail := stderr
	if detail == "" {
		detail = fmt.Sprintf("screencapture produced no image (exit code %d)", exitCode)
	}
	return fmt.Errorf(
		"capture_screen: screen capture failed (%s). The most likely cause is that this app has not been granted Screen Recording. Grant it in System Settings → Privacy & Security → Screen Recording, then try again",
		detail,
	)
}

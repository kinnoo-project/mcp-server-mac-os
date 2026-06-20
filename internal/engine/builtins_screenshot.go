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
// Output-path policy: by default the image is saved into ~/Pictures/Screenshots
// with a generated, collision-resistant name. The caller MAY override the
// destination via output_path, which is validated the same way every other
// model-supplied path is (tilde-expanded by the normalizer, then guarded here
// against a leading '-' so it cannot be parsed as a screencapture flag). To stay
// in the no-confirm read-only lane, capture_screen only ever CREATES files — it
// refuses to overwrite an existing one — so a capture can never destroy data.
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

// screenshotFormat describes one supported output format: the file extension and
// whether stdlib can read its pixel dimensions.
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
	if _, ok := screenshotFormats[format]; !ok {
		return "", fmt.Errorf("capture_screen: unsupported format %q", format)
	}

	// display is optional. When present it must be a positive 1-based index;
	// reject zero/negative up front rather than handing screencapture a value it
	// would treat as "no such display" (a confusing empty capture).
	display, hasDisplay := getInt(in, "display")
	if hasDisplay && display < 1 {
		return "", fmt.Errorf("capture_screen: 'display' must be 1 or greater (1 is the main display), got %d", display)
	}

	// Resolve the destination. resolveScreenshotPath may refine the format when
	// the caller's output_path carries a recognized image extension, so the -t
	// flag always matches the filename actually written.
	outputParam, _ := getString(in, "output_path") // tilde already expanded by the normalizer
	outPath, format, err := resolveScreenshotPath(outputParam, format)
	if err != nil {
		return "", err
	}
	spec := screenshotFormats[format]

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("capture_screen: could not create output directory %s: %w", filepath.Dir(outPath), err)
	}
	// Never overwrite: capture_screen only creates files, which keeps it safely in
	// the read-only lane. Check before capturing so we fail fast.
	if _, err := os.Stat(outPath); err == nil {
		return "", fmt.Errorf("capture_screen: refusing to overwrite existing file %s; choose a different name or omit output_path", outPath)
	}

	bin, err := policy.ResolveBinary("screencapture")
	if err != nil {
		return "", err
	}

	args := screencaptureArgs(format, display, hasDisplay, outPath)
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return "", err
	}

	// Treat a non-zero exit, a missing file, or a zero-byte file as a failed
	// capture — the dominant real-world cause is a denied Screen Recording
	// permission, which screencapture reports this way rather than with a clear
	// error. Remove a stray empty file so the directory does not accumulate them.
	info, statErr := os.Stat(outPath)
	if res.ExitCode != 0 || statErr != nil || info.Size() == 0 {
		if statErr == nil && info.Size() == 0 {
			_ = os.Remove(outPath)
		}
		return "", screencapturePermissionError(res.ExitCode, res.Stderr)
	}

	return reportCapture(outPath, info.Size(), spec.decodable), nil
}

// resolveScreenshotPath decides the output path and the effective image
// format from the optional caller-supplied output_path (already tilde-expanded):
//
//   - empty            → ~/Pictures/Screenshots/<generated-name>, format unchanged
//   - existing dir     → <dir>/<generated-name>, format unchanged
//   - any other value  → treated as a full file path. A recognized image
//     extension on it WINS over the format param (so "x.jpg" reliably yields a
//     JPEG); no extension appends the format's extension; an unknown extension is
//     rejected.
//
// A leading '-' is rejected because the path is screencapture's trailing operand
// and could otherwise be parsed as one of its flags.
func resolveScreenshotPath(outputParam, format string) (path string, effFormat string, err error) {
	if outputParam == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("capture_screen: cannot locate home directory: %w", herr)
		}
		dir := filepath.Join(home, "Pictures", "Screenshots")
		return filepath.Join(dir, generatedScreenshotName(format)), format, nil
	}

	if strings.HasPrefix(outputParam, "-") {
		return "", "", fmt.Errorf("capture_screen: output_path %q must not begin with '-'; prefix it with ./", outputParam)
	}

	// An existing directory means "put the file in here"; we keep ownership of the
	// (generated, collision-resistant) filename.
	if info, statErr := os.Stat(outputParam); statErr == nil && info.IsDir() {
		return filepath.Join(outputParam, generatedScreenshotName(format)), format, nil
	}

	// Otherwise it is a full file path. Let a recognized extension on the chosen
	// filename drive the format so the bytes match the name the user will see.
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(outputParam), "."))
	if ext == "jpeg" {
		ext = "jpg"
	}
	switch {
	case ext == "":
		return outputParam + "." + screenshotFormats[format].ext, format, nil
	default:
		if _, ok := screenshotFormats[ext]; !ok {
			return "", "", fmt.Errorf("capture_screen: unsupported output_path extension %q (supported: png, jpg, pdf, tiff)", ext)
		}
		return outputParam, ext, nil
	}
}

// generatedScreenshotName builds a collision-resistant filename for the given
// format: a nanosecond timestamp means rapid successive captures never collide.
func generatedScreenshotName(format string) string {
	return fmt.Sprintf("screen-%s.%s", time.Now().Format("20060102-150405.000000000"), screenshotFormats[format].ext)
}

// screencaptureArgs assembles the screencapture argument vector. It is factored
// out as a pure function so the exact flag ordering can be unit-tested without
// running the binary. The output path is always the trailing operand; a
// dash-leading path was already rejected in resolveScreenshotPath.
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
// format is one stdlib can decode), and human-readable size.
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
// mirroring messagesDBError/appScriptError. A missing Screen Recording grant is
// a common cause, but stderr detail may indicate another failure.
func screencapturePermissionError(exitCode int, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	detail := stderr
	if detail == "" {
		detail = fmt.Sprintf("screencapture produced no image (exit code %d)", exitCode)
	}
	return fmt.Errorf(
		"capture_screen: screen capture failed (%s). A common cause is missing Screen Recording permission. If needed, grant it in System Settings → Privacy & Security → Screen Recording, then try again",
		detail,
	)
}

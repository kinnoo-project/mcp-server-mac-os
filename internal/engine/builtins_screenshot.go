// builtins_screenshot.go implements the read-only screen-capture builtins:
// capture_screen (whole desktop), and — through the shared spine defined here —
// capture_region and capture_window (builtins_screenshot_region.go). All three
// photograph the screen with macOS's screencapture(1) and report where the image
// was saved plus its dimensions and size.
//
// They are "builtins" (run in-process, like list_printers) rather than plain
// argv-builders for one reason: screencapture frequently exits 0 even when the
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
// in the no-confirm read-only lane, a capture only ever CREATES files — it
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

// captureRegion is an optional rectangular crop, in global screen points, for
// screencapture's -R flag. capture_region supplies it from four validated ints;
// capture_window supplies it from a window's probed bounds; capture_screen passes
// nil (whole desktop). A nil *captureRegion therefore means "no -R".
type captureRegion struct {
	x, y, w, h int
}

// runCaptureScreen captures the whole desktop and returns a human-readable
// summary of where the image landed and its dimensions/size. A denied Screen
// Recording permission (which screencapture reports as an empty/zero-byte file,
// often with exit 0) is surfaced as an actionable grant message.
//
// It is a thin wrapper over runCapture, the spine shared with capture_region and
// capture_window: capture_screen is simply the case with no region crop and an
// optional -D display selection.
func runCaptureScreen(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	// display is optional. When present it must be a positive 1-based index;
	// reject zero/negative up front rather than handing screencapture a value it
	// would treat as "no such display" (a confusing empty capture).
	display, hasDisplay := getInt(in, "display")
	if hasDisplay && display < 1 {
		return "", fmt.Errorf("capture_screen: 'display' must be 1 or greater (1 is the main display), got %d", display)
	}
	return runCapture(ctx, captureRequest{
		op:         "capture_screen",
		in:         in,
		display:    display,
		hasDisplay: hasDisplay,
	})
}

// captureRequest bundles everything the shared capture spine needs. Each caller
// (capture_screen / capture_region / capture_window) fills in only the fields its
// grammar uses: capture_screen sets display; the region captures set regionFn;
// all three read format and output_path straight from in.
type captureRequest struct {
	op         string         // capability name, used in error messages
	in         map[string]any // normalized params (format, output_path already tilde-expanded)
	display    int            // 1-based display index for -D (capture_screen only)
	hasDisplay bool           // whether display was supplied
	// regionFn, when non-nil, computes the -R crop. It is called only AFTER the
	// cheap input validation (format, output_path, no-overwrite) has passed, so a
	// caller that must do permission-gated work to find the rectangle
	// (capture_window's System Events probe) never triggers a permission prompt
	// for a request that was going to be rejected on its output_path anyway. A nil
	// regionFn means "no -R" (capture the whole display).
	regionFn func() (*captureRegion, error)
}

// runCapture is the shared spine of all three screenshot builtins. It resolves
// and guards the output path, enforces the create-only (no-overwrite) contract
// that keeps captures in the read-only lane, runs screencapture with the caller's
// region/display selection, and turns a silent Screen Recording denial (an empty
// or missing file, often with exit 0) into an actionable grant message.
//
// Ordering matters: all the cheap, non-prompting validation (format, output path,
// overwrite check) happens BEFORE regionFn is invoked, so a permission-gated crop
// computation (capture_window's window-bounds probe) cannot fire — and cannot
// surface a permission prompt — for a request that a dash-leading or occupied
// output_path would reject regardless.
func runCapture(ctx context.Context, r captureRequest) (string, error) {
	format, _ := getString(r.in, "format") // enum-validated by the normalizer; defaults to "png"
	if _, ok := screenshotFormats[format]; !ok {
		return "", fmt.Errorf("%s: unsupported format %q", r.op, format)
	}

	// Resolve the destination. resolveScreenshotPath may refine the format when
	// the caller's output_path carries a recognized image extension, so the -t
	// flag always matches the filename actually written.
	outputParam, _ := getString(r.in, "output_path") // tilde already expanded by the normalizer
	outPath, format, err := resolveScreenshotPath(r.op, outputParam, format)
	if err != nil {
		return "", err
	}
	spec := screenshotFormats[format]

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("%s: could not create output directory %s: %w", r.op, filepath.Dir(outPath), err)
	}
	// Never overwrite: a capture only creates files, which keeps it safely in the
	// read-only lane. Check before capturing so we fail fast.
	if _, err := os.Stat(outPath); err == nil {
		return "", fmt.Errorf("%s: refusing to overwrite existing file %s; choose a different name or omit output_path", r.op, outPath)
	}

	// Only now — after the cheap validation has passed — compute the crop. For
	// capture_window this is the permission-gated System Events probe.
	var region *captureRegion
	if r.regionFn != nil {
		region, err = r.regionFn()
		if err != nil {
			return "", err
		}
	}

	bin, err := policy.ResolveBinary("screencapture")
	if err != nil {
		return "", err
	}

	args := screencaptureArgs(format, r.display, r.hasDisplay, region, outPath)
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
		return "", screencapturePermissionError(r.op, res.ExitCode, res.Stderr)
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
// and could otherwise be parsed as one of its flags. op names the calling
// capability so its errors read in that capability's terms.
func resolveScreenshotPath(op, outputParam, format string) (path string, effFormat string, err error) {
	if outputParam == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("%s: cannot locate home directory: %w", op, herr)
		}
		dir := filepath.Join(home, "Pictures", "Screenshots")
		return filepath.Join(dir, generatedScreenshotName(format)), format, nil
	}

	if strings.HasPrefix(outputParam, "-") {
		return "", "", fmt.Errorf("%s: output_path %q must not begin with '-'; prefix it with ./", op, outputParam)
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
			return "", "", fmt.Errorf("%s: unsupported output_path extension %q (supported: png, jpg, pdf, tiff)", op, ext)
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
// running the binary. A non-nil region adds the -R x,y,w,h crop (used by
// capture_region/capture_window); it is mutually exclusive with -D in practice,
// but the two are simply appended in order. The output path is always the
// trailing operand; a dash-leading path was already rejected in
// resolveScreenshotPath, and the region ints are validated before this call.
func screencaptureArgs(format string, display int, hasDisplay bool, region *captureRegion, outPath string) []string {
	// -x suppresses the capture sound (there is no human at the prompt).
	// -t selects the image format.
	args := []string{"-x", "-t", format}
	if region != nil {
		// -R crops to a rectangle in global screen points: x,y is the top-left,
		// then width,height. screencapture takes it as a single comma-joined value.
		args = append(args, "-R", fmt.Sprintf("%d,%d,%d,%d", region.x, region.y, region.w, region.h))
	}
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
// a common cause, but stderr detail may indicate another failure. op names the
// calling capability so the message reads in its terms.
func screencapturePermissionError(op string, exitCode int, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	detail := stderr
	if detail == "" {
		detail = fmt.Sprintf("screencapture produced no image (exit code %d)", exitCode)
	}
	return fmt.Errorf(
		"%s: screen capture failed (%s). A common cause is missing Screen Recording permission. If needed, grant it in System Settings → Privacy & Security → Screen Recording, then try again",
		op, detail,
	)
}

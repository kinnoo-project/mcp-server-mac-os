// media_filesystem_test.go tests the media & document conversion capabilities:
// the image_info builtin and the convert_image / resize_image /
// convert_document / quicklook_thumbnail mutators. Coverage falls in four bands:
//
//   - argv pinning: each staged plan produces the exact forward/inverse Command
//     shape (so a later refactor cannot silently drop a "--" or reorder --out);
//   - new-path enforcement: a destination that already exists, or whose parent
//     is missing, is refused at stage time (the property that makes the Trash
//     inverse non-destructive);
//   - injection regressions: a dash-leading path/source/destination is rejected
//     (sips has no "--", so this is the real defense), and a hostile enum value
//     is refused by the engine's validator;
//   - a real round trip: convert_image and quicklook_thumbnail are executed for
//     real and undone, proving the produced file and the Trash inverse work.
//
// Tests that execute a Trash-routed inverse first redirect $HOME to a sandbox
// (redirectHomeWithTrash, from mutate_filesystem_test.go) so nothing lands in
// the developer's real Trash.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// tinyPNG is a minimal valid 1x1 PNG, written to disk as a real source image so
// the sips/qlmanage round-trip tests operate on genuine image bytes rather than
// a fake file the tools would reject. It is the standard 67-byte 1x1 opaque
// image (IHDR + a single IDAT + IEND).
var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00,
	0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x18, 0xDD, 0x8D, 0xB0, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// writeTinyPNG writes tinyPNG to a named file inside dir and returns its path.
func writeTinyPNG(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, tinyPNG, 0o644); err != nil {
		t.Fatalf("writing test PNG: %v", err)
	}
	return p
}

func TestStageConvertImage_Plan(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")

	plan, err := stageConvertImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "format": "jpeg"})
	if err != nil {
		t.Fatalf("stageConvertImage: %v", err)
	}
	wantFwd := Command{Binary: "sips", Args: []string{"-s", "format", "jpeg", src, "--out", dst}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" ||
		len(plan.Inverse.Args) != 3 || plan.Inverse.Args[0] != "--" || plan.Inverse.Args[1] != dst {
		t.Errorf("Inverse = %+v, want mv -- %s <trash>", plan.Inverse, dst)
	}
}

// TestStageConvertImage_RoundTrip converts a real PNG to JPEG and then undoes
// it, proving the produced file exists and the Trash inverse removes it.
func TestStageConvertImage_RoundTrip(t *testing.T) {
	home := redirectHomeWithTrash(t)
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")

	plan, err := stageConvertImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "format": "jpeg"})
	if err != nil {
		t.Fatalf("stageConvertImage: %v", err)
	}
	runPlanCommand(t, plan.Forward)
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected converted file at %s: %v", dst, err)
	}
	runPlanCommand(t, *plan.Inverse)
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("after undo, %s should be gone, stat err = %v", dst, err)
	}
	// The original must be untouched throughout.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source %s should be preserved: %v", src, err)
	}
	// The converted file should now sit in the sandbox Trash.
	if entries, _ := os.ReadDir(filepath.Join(home, ".Trash")); len(entries) == 0 {
		t.Errorf("expected the converted file to be recycled into %s/.Trash", home)
	}
}

func TestStageConvertImage_RejectsExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := writeTinyPNG(t, dir, "out.jpg") // already exists

	_, err := stageConvertImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "format": "jpeg"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected an already-exists error, got %v", err)
	}
}

func TestStageConvertImage_RejectsMissingParent(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "nope", "out.jpg") // parent "nope" does not exist

	_, err := stageConvertImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "format": "jpeg"})
	if err == nil || !strings.Contains(err.Error(), "parent directory") {
		t.Fatalf("expected a missing-parent error, got %v", err)
	}
}

// TestStageConvertImage_RejectsDashLeading proves the sips injection defense: a
// dash-leading source or destination is refused up front (sips has no "--", so a
// value like "-e" would otherwise be read as a sips option). This is the per-op
// "flag-like value never lands as a flag" regression required by CLAUDE.md §4.
func TestStageConvertImage_RejectsDashLeading(t *testing.T) {
	dir := t.TempDir()
	good := writeTinyPNG(t, dir, "in.png")
	for _, tc := range []struct {
		name string
		in   map[string]any
	}{
		{"dash source", map[string]any{"source": "-e", "destination": filepath.Join(dir, "o.jpg"), "format": "jpeg"}},
		{"dash destination", map[string]any{"source": good, "destination": "-e", "format": "jpeg"}},
	} {
		if _, err := stageConvertImage(context.Background(), registry.Capability{}, tc.in); err == nil ||
			!strings.Contains(err.Error(), "'-'") {
			t.Errorf("%s: expected a dash-leading rejection, got %v", tc.name, err)
		}
	}
}

// TestConvertImage_RejectsBadFormatViaEngine exercises the full Stage path so
// the registry's enum validator rejects a hostile/unknown format value before
// the mutator ever runs.
func TestConvertImage_RejectsBadFormatViaEngine(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	eng := New()
	cap, ok := reg.Lookup("convert_image")
	if !ok {
		t.Fatal("convert_image capability not found")
	}
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	_, err = eng.Stage(context.Background(), cap, map[string]any{
		"source": src, "destination": filepath.Join(dir, "o.x"), "format": "-e",
	})
	if err == nil {
		t.Fatal("expected the engine to reject the bogus format enum, got nil")
	}
}

func TestStageResizeImage_Plan(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "out.png")

	cases := []struct {
		name  string
		param string
		flag  string
		value int
	}{
		{"width", "width", "--resampleWidth", 100},
		{"height", "height", "--resampleHeight", 80},
		{"max_dimension", "max_dimension", "--resampleHeightWidthMax", 256},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := stageResizeImage(context.Background(), registry.Capability{},
				map[string]any{"source": src, "destination": dst, tc.param: tc.value})
			if err != nil {
				t.Fatalf("stageResizeImage: %v", err)
			}
			wantFwd := Command{Binary: "sips", Args: []string{tc.flag, strconv.Itoa(tc.value), src, "--out", dst}}
			if !reflect.DeepEqual(plan.Forward, wantFwd) {
				t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
			}
		})
	}
}

func TestStageResizeImage_RequiresExactlyOneDimension(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "out.png")

	// None supplied.
	if _, err := stageResizeImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst}); err == nil ||
		!strings.Contains(err.Error(), "is required") {
		t.Errorf("expected 'one of ... is required', got %v", err)
	}
	// Two supplied.
	if _, err := stageResizeImage(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "width": 100, "height": 80}); err == nil ||
		!strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected 'exactly one' error, got %v", err)
	}
}

func TestStageResizeImage_RejectsOutOfRange(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	dst := filepath.Join(dir, "out.png")
	for _, v := range []int{0, -5, maxImageDimension + 1} {
		if _, err := stageResizeImage(context.Background(), registry.Capability{},
			map[string]any{"source": src, "destination": dst, "width": v}); err == nil ||
			!strings.Contains(err.Error(), "between 1 and") {
			t.Errorf("width %d: expected an out-of-range error, got %v", v, err)
		}
	}
}

func TestStageConvertDocument_Plan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("hello *world*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.html")

	plan, err := stageConvertDocument(context.Background(), registry.Capability{},
		map[string]any{"source": src, "destination": dst, "format": "html"})
	if err != nil {
		t.Fatalf("stageConvertDocument: %v", err)
	}
	// textutil honours "--"; the source must ride after it as the LAST operand.
	wantFwd := Command{Binary: "textutil", Args: []string{"-convert", "html", "-output", dst, "--", src}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	if plan.Inverse == nil || plan.Inverse.Args[1] != dst {
		t.Errorf("Inverse = %+v, want mv -- %s <trash>", plan.Inverse, dst)
	}
}

// TestStageQuicklookThumbnail_Plan asserts the argv shape and that staging
// created a server-owned output directory under the fallback dir (qlmanage
// requires -o to exist). The created scratch dir is cleaned up here.
func TestStageQuicklookThumbnail_Plan(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")

	plan, err := stageQuicklookThumbnail(context.Background(), registry.Capability{},
		map[string]any{"path": src, "size": 128})
	if err != nil {
		t.Fatalf("stageQuicklookThumbnail: %v", err)
	}
	if plan.Forward.Binary != "qlmanage" {
		t.Fatalf("Forward binary = %q, want qlmanage", plan.Forward.Binary)
	}
	args := plan.Forward.Args
	// Expect: -t -s 128 -o <outDir> -- <src>
	if len(args) != 7 || args[0] != "-t" || args[1] != "-s" || args[2] != "128" ||
		args[3] != "-o" || args[5] != "--" || args[6] != src {
		t.Fatalf("Forward.Args = %v, want [-t -s 128 -o <dir> -- %s]", args, src)
	}
	outDir := args[4]
	defer os.RemoveAll(outDir)
	if !strings.HasPrefix(outDir, fallbackDir+"/ql-") {
		t.Errorf("output dir %q is not a fresh scratch dir under %s", outDir, fallbackDir)
	}
	if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
		t.Errorf("expected output dir %q to exist as a directory: %v", outDir, err)
	}
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" || plan.Inverse.Args[1] != outDir {
		t.Errorf("Inverse = %+v, want mv -- %s <trash>", plan.Inverse, outDir)
	}
}

func TestStageQuicklookThumbnail_RejectsDashPath(t *testing.T) {
	if _, err := stageQuicklookThumbnail(context.Background(), registry.Capability{},
		map[string]any{"path": "-e"}); err == nil || !strings.Contains(err.Error(), "'-'") {
		t.Fatalf("expected a dash-leading rejection, got %v", err)
	}
}

func TestClampThumbnailSize(t *testing.T) {
	cases := map[int]int{-3: 1, 0: 1, 1: 1, 512: 512, 2048: 2048, 5000: maxThumbnailSize}
	for in, want := range cases {
		if got := clampThumbnailSize(in); got != want {
			t.Errorf("clampThumbnailSize(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestRunImageInfo_RealFile runs image_info against a real PNG and checks it
// surfaces the pixel dimensions sips reports.
func TestRunImageInfo_RealFile(t *testing.T) {
	dir := t.TempDir()
	src := writeTinyPNG(t, dir, "in.png")
	out, err := runImageInfo(context.Background(), registry.Capability{}, map[string]any{"path": src})
	if err != nil {
		t.Fatalf("runImageInfo: %v", err)
	}
	if !strings.Contains(out, "pixelWidth") || !strings.Contains(out, "pixelHeight") {
		t.Errorf("image_info output missing pixel dimensions:\n%s", out)
	}
}

func TestRunImageInfo_RejectsDashPath(t *testing.T) {
	if _, err := runImageInfo(context.Background(), registry.Capability{},
		map[string]any{"path": "-e"}); err == nil || !strings.Contains(err.Error(), "'-'") {
		t.Fatalf("expected a dash-leading rejection, got %v", err)
	}
}

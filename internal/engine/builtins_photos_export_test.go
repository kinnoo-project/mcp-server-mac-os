// builtins_photos_export_test.go tests the subprocess-free logic of export_photo:
// the dash-leading destination guard (which returns before any osascript call or
// directory creation), the listing/reporting of exported files, and the boolean
// argv rendering. The osascript "--" terminator that carries the id/destination is
// the shared seam proven in injection_sweep_test.go and is not re-driven here.
//
// SAFETY: nothing here launches osascript or touches the real Photos app.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestExportPhoto_RejectsDashLeadingDestination verifies a destination beginning
// with '-' is refused up front — before any directory is created or osascript is
// invoked — so it can never be mistaken for a flag.
func TestExportPhoto_RejectsDashLeadingDestination(t *testing.T) {
	_, err := runExportPhoto(context.Background(), registry.Capability{}, map[string]any{
		"id":          "ABC123/L0/001",
		"destination": "-rf",
	})
	if err == nil || !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Fatalf("expected dash-leading destination to be rejected, got err=%v", err)
	}
}

// TestExportPhoto_RequiresID verifies a missing id is refused.
func TestExportPhoto_RequiresID(t *testing.T) {
	if _, err := runExportPhoto(context.Background(), registry.Capability{}, map[string]any{}); err == nil {
		t.Fatalf("expected missing id to be rejected")
	}
}

// TestExportedFilesAndReport verifies the directory scan ignores subdirectories,
// sorts files by name, and that the report lists each file with its full path.
func TestExportedFilesAndReport(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "b.jpg"), "world!")
	mustWrite(t, filepath.Join(dir, "a.heic"), "hello")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	files, err := exportedFiles(dir)
	if err != nil {
		t.Fatalf("exportedFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files (subdir ignored), got %d: %+v", len(files), files)
	}
	if files[0].name != "a.heic" || files[1].name != "b.jpg" {
		t.Errorf("expected name-sorted [a.heic b.jpg], got [%s %s]", files[0].name, files[1].name)
	}

	out := reportExport(dir, files)
	for _, want := range []string{"Exported 2 file(s)", "a.heic", "b.jpg", filepath.Join(dir, "a.heic")} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// TestBoolText covers the argv rendering the export script compares against.
func TestBoolText(t *testing.T) {
	if boolText(true) != "true" || boolText(false) != "false" {
		t.Errorf("boolText: got %q/%q", boolText(true), boolText(false))
	}
}

// mustWrite writes content to path or fails the test.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

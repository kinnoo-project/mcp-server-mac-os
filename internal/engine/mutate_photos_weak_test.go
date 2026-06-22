// mutate_photos_weak_test.go tests the no-undo Photos mutators. Unlike the
// reversible ones, these stage WITHOUT a live probe, so the full forward command
// and preview are unit-testable here — including the option-injection regression
// (a flag-like value must land as "--"-terminated data) and the invariant that
// every plan is irreversible (Inverse == nil) with an honest "cannot be undone"
// preview.
//
// SAFETY: no test executes a StagedPlan, so no album/folder is created and nothing
// is imported.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestStageCreateAlbum_ForwardAndPreview checks the forward argv (name + parent
// after the terminator) and that the plan is irreversible with a clear notice.
func TestStageCreateAlbum_ForwardAndPreview(t *testing.T) {
	plan, err := stageCreateAlbum(context.Background(), lookupCapability(t, "create_album"),
		map[string]any{"name": "Trip 2024", "parent_folder": "Travel"})
	if err != nil {
		t.Fatalf("stageCreateAlbum: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("create_album must be irreversible: Inverse should be nil")
	}
	fa := plan.Forward.Args
	// ["-e", createAlbumScript, "--", name, parent]
	if plan.Forward.Binary != "osascript" || len(fa) != 5 || fa[2] != "--" || fa[3] != "Trip 2024" || fa[4] != "Travel" {
		t.Fatalf("unexpected forward argv: %v", fa)
	}
	for _, want := range []string{"Trip 2024", "Travel", "cannot be undone"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

// TestStageAddToAlbum_FlagLikeValuesStayData is the option-injection regression:
// a flag-like album name and id must land as data after the "--" terminator.
func TestStageAddToAlbum_FlagLikeValuesStayData(t *testing.T) {
	plan, err := stageAddToAlbum(context.Background(), lookupCapability(t, "add_to_album"),
		map[string]any{"album": "-e", "ids": []string{"-rf", "ABC123"}})
	if err != nil {
		t.Fatalf("stageAddToAlbum: %v", err)
	}
	fa := plan.Forward.Args
	// ["-e", addToAlbumScript, "--", album, id1, id2]
	if fa[2] != "--" || fa[3] != "-e" || fa[4] != "-rf" || fa[5] != "ABC123" {
		t.Fatalf("flag-like values not neutralized: %v", fa)
	}
	if plan.Inverse != nil {
		t.Error("add_to_album must be irreversible: Inverse should be nil")
	}
	if !strings.Contains(plan.Preview, "2 item(s)") {
		t.Errorf("preview should report the item count: %s", plan.Preview)
	}
}

// TestStageAddToAlbum_Validation covers the missing-album, empty-ids, and
// empty-id-element rejections.
func TestStageAddToAlbum_Validation(t *testing.T) {
	cap := lookupCapability(t, "add_to_album")
	cases := []map[string]any{
		{"ids": []string{"ABC"}},                    // missing album
		{"album": "X"},                              // missing ids
		{"album": "X", "ids": []string{}},           // empty ids
		{"album": "X", "ids": []string{"ABC", " "}}, // empty id element
	}
	for _, in := range cases {
		if _, err := stageAddToAlbum(context.Background(), cap, in); err == nil {
			t.Errorf("expected rejection for %v", in)
		}
	}
}

// TestStageImportPhotos_ExistingFiles verifies a real file stages a forward import
// with the album-then-files argv layout and an irreversible plan, while a missing
// file is rejected at stage time.
func TestStageImportPhotos_ExistingFiles(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.jpg")
	if err := os.WriteFile(img, []byte("not really a jpeg"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	plan, err := stageImportPhotos(context.Background(), lookupCapability(t, "import_photos"),
		map[string]any{"files": []string{img}, "album": "Imports"})
	if err != nil {
		t.Fatalf("stageImportPhotos: %v", err)
	}
	fa := plan.Forward.Args
	// ["-e", importPhotosScript, "--", album, file1]
	if fa[2] != "--" || fa[3] != "Imports" || fa[4] != img {
		t.Fatalf("unexpected forward argv: %v", fa)
	}
	if plan.Inverse != nil {
		t.Error("import_photos must be irreversible: Inverse should be nil")
	}

	// A non-existent file is refused before staging.
	if _, err := stageImportPhotos(context.Background(), lookupCapability(t, "import_photos"),
		map[string]any{"files": []string{filepath.Join(dir, "missing.jpg")}}); err == nil {
		t.Error("expected a missing file to be rejected")
	}

	// A non-regular file (here a directory) is refused: import requires a real
	// image/video file, not a directory or special file.
	if _, err := stageImportPhotos(context.Background(), lookupCapability(t, "import_photos"),
		map[string]any{"files": []string{dir}}); err == nil {
		t.Error("expected a non-regular file (directory) to be rejected")
	}
}

// TestStageCreateFolder_TopLevel verifies an omitted parent stages as an empty
// parent argument (the script's top-level branch).
func TestStageCreateFolder_TopLevel(t *testing.T) {
	plan, err := stageCreateFolder(context.Background(), registry.Capability{}, map[string]any{"name": "2024"})
	if err != nil {
		t.Fatalf("stageCreateFolder: %v", err)
	}
	if plan.Forward.Args[4] != "" {
		t.Errorf("top-level folder should stage an empty parent, got %q", plan.Forward.Args[4])
	}
}

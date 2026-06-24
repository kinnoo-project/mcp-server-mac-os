// mutate_filesystem_test.go tests the three reversible file mutators — move,
// copy, and remove — covering both the staged plan they produce and a real
// forward→inverse round trip on disk.
//
// remove and copy reverse through the user's Trash, so every test that executes
// those plans first redirects $HOME to a temp directory (os.UserHomeDir reads
// $HOME on Darwin) and creates a .Trash there. This keeps the real Trash
// untouched while still exercising the genuine commands end to end.
package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runPlanCommand executes one staged Command for real, resolving the binary the
// same way production does. It is how the round-trip tests assert reversibility.
func runPlanCommand(t *testing.T, cmd Command) {
	t.Helper()
	bin, err := policy.ResolveBinary(cmd.Binary)
	if err != nil {
		t.Fatalf("resolving %q: %v", cmd.Binary, err)
	}
	if out, err := exec.Command(bin, cmd.Args...).CombinedOutput(); err != nil {
		t.Fatalf("running %s %v: %v (%s)", cmd.Binary, cmd.Args, err, out)
	}
}

// redirectHomeWithTrash points $HOME at a fresh temp dir containing a .Trash, so
// the Trash-routed mutators recycle into a sandbox rather than the real Trash.
func redirectHomeWithTrash(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".Trash"), 0o755); err != nil {
		t.Fatalf("creating sandbox Trash: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestStageMove_PlanAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "renamed.txt")

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": dst})
	if err != nil {
		t.Fatalf("stageMove: %v", err)
	}
	wantFwd := Command{Binary: "mv", Args: []string{"--", src, dst}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	wantInv := Command{Binary: "mv", Args: []string{"--", dst, src}}
	if plan.Inverse == nil || !reflect.DeepEqual(*plan.Inverse, wantInv) {
		t.Errorf("Inverse = %+v, want %+v", plan.Inverse, wantInv)
	}

	// Forward moves the file; inverse must put it back exactly.
	runPlanCommand(t, plan.Forward)
	if pathExists(src) || !pathExists(dst) {
		t.Fatalf("after forward: src exists=%v dst exists=%v", pathExists(src), pathExists(dst))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(src) || pathExists(dst) {
		t.Fatalf("after undo: src exists=%v dst exists=%v", pathExists(src), pathExists(dst))
	}
}

// TestStageMove_IntoExistingDir confirms the "move X into folder Y" semantics:
// when the destination is an existing directory, the final path is Y/basename(X)
// — this is the case from the bug report ("move test.txt to the Desktop folder").
func TestStageMove_IntoExistingDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "Desktop")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": destDir})
	if err != nil {
		t.Fatalf("stageMove: %v", err)
	}
	wantFinal := filepath.Join(destDir, "test.txt")
	if plan.Forward.Args[len(plan.Forward.Args)-1] != wantFinal {
		t.Errorf("final destination = %q, want %q", plan.Forward.Args[len(plan.Forward.Args)-1], wantFinal)
	}
}

func TestStageMove_Rejects(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	occupied := filepath.Join(dir, "b.txt")
	for _, p := range []string{existing, occupied} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := map[string]map[string]any{
		"missing source":      {"destination": filepath.Join(dir, "new.txt")},
		"missing destination": {"source": existing},
		"nonexistent source":  {"source": filepath.Join(dir, "ghost.txt"), "destination": filepath.Join(dir, "new.txt")},
		"overwrite refused":   {"source": existing, "destination": occupied},
		"dash source":         {"source": "-rf", "destination": filepath.Join(dir, "new.txt")},
		"dash destination":    {"source": existing, "destination": "-x"},
		// mv/cp do not create intermediate directories, so a destination whose
		// parent does not exist must be rejected at stage time (not surface later
		// as a failed forward command).
		"missing dest parent": {"source": existing, "destination": filepath.Join(dir, "no_such_dir", "new.txt")},
		// A destination parent that exists but is a regular file (not a directory)
		// is equally doomed and must be rejected.
		"dest parent not dir": {"source": existing, "destination": filepath.Join(occupied, "new.txt")},
	}
	for name, in := range cases {
		if _, err := stageMove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageMoveGlob_PlanAndRoundTrip exercises the batch (source_glob) move: many
// files selected by a pattern, moved into one directory, then fully restored by
// the single inverse command.
func TestStageMoveGlob_PlanAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	names := []string{"shot-a.png", "shot-b.png", "shot-c.png", "notes.txt"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	destDir := filepath.Join(dir, "screenshots")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{
		"source_glob": filepath.Join(dir, "shot-*.png"),
		"destination": destDir,
	})
	if err != nil {
		t.Fatalf("stageMove (glob): %v", err)
	}

	// Forward must move exactly the three png matches (sorted) into destDir, with a
	// single "--" terminator and the directory as the trailing argument.
	wantFwd := Command{Binary: "mv", Args: []string{
		"--",
		filepath.Join(dir, "shot-a.png"),
		filepath.Join(dir, "shot-b.png"),
		filepath.Join(dir, "shot-c.png"),
		destDir,
	}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	wantInv := Command{Binary: "mv", Args: []string{
		"--",
		filepath.Join(destDir, "shot-a.png"),
		filepath.Join(destDir, "shot-b.png"),
		filepath.Join(destDir, "shot-c.png"),
		dir,
	}}
	if plan.Inverse == nil || !reflect.DeepEqual(*plan.Inverse, wantInv) {
		t.Errorf("Inverse = %+v, want %+v", plan.Inverse, wantInv)
	}

	// Round trip: forward moves the three pngs into destDir (notes.txt stays put),
	// inverse restores them.
	runPlanCommand(t, plan.Forward)
	for _, n := range []string{"shot-a.png", "shot-b.png", "shot-c.png"} {
		if pathExists(filepath.Join(dir, n)) || !pathExists(filepath.Join(destDir, n)) {
			t.Fatalf("after forward, %s not moved into destDir", n)
		}
	}
	if !pathExists(filepath.Join(dir, "notes.txt")) {
		t.Fatalf("non-matching notes.txt should not have moved")
	}
	runPlanCommand(t, *plan.Inverse)
	for _, n := range []string{"shot-a.png", "shot-b.png", "shot-c.png"} {
		if !pathExists(filepath.Join(dir, n)) || pathExists(filepath.Join(destDir, n)) {
			t.Fatalf("after undo, %s not restored to its parent", n)
		}
	}
}

// TestStageMoveGlob_Rejects covers the guardrails specific to a batch move.
func TestStageMoveGlob_Rejects(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "out")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file occupying a destination name, to force a collision rejection.
	if err := os.WriteFile(filepath.Join(destDir, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second source directory, to force the shared-parent rejection.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "c.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]map[string]any{
		"both source and glob": {"source": filepath.Join(dir, "a.png"), "source_glob": filepath.Join(dir, "*.png"), "destination": destDir},
		"no matches":           {"source_glob": filepath.Join(dir, "nope-*.png"), "destination": destDir},
		"dash glob":            {"source_glob": "-rf*", "destination": destDir},
		"dest not existing":    {"source_glob": filepath.Join(dir, "*.png"), "destination": filepath.Join(dir, "ghost_dir")},
		"dest is a file":       {"source_glob": filepath.Join(dir, "*.png"), "destination": filepath.Join(dir, "a.png")},
		"dest equals parent":   {"source_glob": filepath.Join(dir, "*.png"), "destination": dir},
		"collision at dest":    {"source_glob": filepath.Join(dir, "a.png"), "destination": destDir},
	}
	for name, in := range cases {
		if _, err := stageMove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageMove_WhitespaceHint confirms the diagnostic that fires when a single
// source path misses only because the typed name uses a different kind of space
// than the real file (the macOS-screenshot U+202F problem). The error must point
// at the real file and mention the narrow no-break space so the model knows to
// reach for source_glob.
func TestStageMove_WhitespaceHint(t *testing.T) {
	dir := t.TempDir()
	// Real file uses a narrow no-break space (U+202F) before "PM".
	actual := filepath.Join(dir, "Screenshot 2026-06-23 at 1.00.00 PM.png")
	if err := os.WriteFile(actual, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Caller supplies the same name but with an ordinary space.
	typed := filepath.Join(dir, "Screenshot 2026-06-23 at 1.00.00 PM.png")

	_, err := stageMove(context.Background(), registry.Capability{}, map[string]any{
		"source":      typed,
		"destination": filepath.Join(dir, "moved.png"),
	})
	if err == nil {
		t.Fatal("expected an error for the mistyped name")
	}
	msg := err.Error()
	if !strings.Contains(msg, "narrow no-break space") || !strings.Contains(msg, "source_glob") {
		t.Errorf("error message lacks the whitespace hint: %q", msg)
	}
}

func TestStageCopy_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.txt")

	plan, err := stageCopy(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": dst})
	if err != nil {
		t.Fatalf("stageCopy: %v", err)
	}
	wantFwd := Command{Binary: "cp", Args: []string{"-R", "--", src, dst}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	// Inverse moves the freshly-made copy to the Trash (never an rm).
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" || plan.Inverse.Args[len(plan.Inverse.Args)-2] != dst {
		t.Fatalf("Inverse = %+v, want mv of copy into Trash", plan.Inverse)
	}

	runPlanCommand(t, plan.Forward)
	if !pathExists(src) || !pathExists(dst) {
		t.Fatalf("after copy: original and copy should both exist (src=%v dst=%v)", pathExists(src), pathExists(dst))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(src) {
		t.Errorf("undo must not touch the original")
	}
	if pathExists(dst) {
		t.Errorf("undo should have removed the copy from %q", dst)
	}
	trashDest := plan.Inverse.Args[len(plan.Inverse.Args)-1]
	if !pathExists(trashDest) {
		t.Errorf("copy should now be in the Trash at %q", trashDest)
	}
}

func TestStageRemove_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := stageRemove(context.Background(), registry.Capability{}, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("stageRemove: %v", err)
	}
	// Forward recycles to the Trash (mv), never a hard rm.
	if plan.Forward.Binary != "mv" || plan.Forward.Args[len(plan.Forward.Args)-2] != target {
		t.Fatalf("Forward = %+v, want mv of target into Trash", plan.Forward)
	}
	trashDest := plan.Forward.Args[len(plan.Forward.Args)-1]

	runPlanCommand(t, plan.Forward)
	if pathExists(target) || !pathExists(trashDest) {
		t.Fatalf("after remove: target gone=%v in trash=%v", !pathExists(target), pathExists(trashDest))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(target) || pathExists(trashDest) {
		t.Fatalf("after undo: target restored=%v trash empty=%v", pathExists(target), !pathExists(trashDest))
	}
}

func TestStageRemove_RejectsMissingAndDash(t *testing.T) {
	dir := t.TempDir()
	for name, in := range map[string]map[string]any{
		"missing path":     {},
		"nonexistent path": {"path": filepath.Join(dir, "ghost.txt")},
		"dash path":        {"path": "-rf"},
	} {
		if _, err := stageRemove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestTrashPathFor_MissingTrashDir confirms staging fails with a clear error
// (rather than producing a doomed plan) when ~/.Trash is absent or is not a
// directory — the case Copilot flagged on PR #19. Both a missing directory and a
// regular file sitting where the Trash should be must be rejected.
func TestTrashPathFor_MissingTrashDir(t *testing.T) {
	// $HOME with no .Trash at all.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := trashPathFor("/somewhere/test.txt"); err == nil {
		t.Errorf("expected error when ~/.Trash is missing, got nil")
	}

	// $HOME where .Trash exists but is a regular file, not a directory.
	if err := os.WriteFile(filepath.Join(home, ".Trash"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := trashPathFor("/somewhere/test.txt"); err == nil {
		t.Errorf("expected error when ~/.Trash is not a directory, got nil")
	}
}

// TestStageRemove_FailsWhenTrashMissing confirms the validation surfaces through
// a real mutator: removing a file fails at STAGE time (not later) when there is
// nowhere to recycle to.
func TestStageRemove_FailsWhenTrashMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no .Trash created
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageRemove(context.Background(), registry.Capability{}, map[string]any{"path": target}); err == nil {
		t.Errorf("stageRemove should fail when ~/.Trash is unavailable")
	}
}

// TestTrashPathFor_CollisionSuffix confirms an existing same-named item in the
// Trash is not overwritten: the helper appends a numeric suffix instead.
func TestTrashPathFor_CollisionSuffix(t *testing.T) {
	home := redirectHomeWithTrash(t)
	occupied := filepath.Join(home, ".Trash", "test.txt")
	if err := os.WriteFile(occupied, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := trashPathFor("/somewhere/test.txt")
	if err != nil {
		t.Fatalf("trashPathFor: %v", err)
	}
	want := filepath.Join(home, ".Trash", "test 2.txt")
	if got != want {
		t.Errorf("trashPathFor with collision = %q, want %q", got, want)
	}
}

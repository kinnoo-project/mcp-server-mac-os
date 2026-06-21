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
	}
	for name, in := range cases {
		if _, err := stageMove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
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

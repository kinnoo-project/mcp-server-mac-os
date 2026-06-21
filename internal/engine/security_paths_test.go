// security_paths_test.go is part of the production security gate (see
// docs/TESTS.md). It pins the filesystem mutators' safety properties that keep a
// delete or move from destroying data irrecoverably:
//
//   - removal and the undo of a copy recycle THROUGH the user's Trash (an `mv`
//     into ~/.Trash), never a hard `rm` — so there is always a manual restore
//     path even if the transactional undo is lost.
//   - a path carrying shell metacharacters is emitted as a single verbatim argv
//     operand after the "--" terminator, proving the no-shell + option-injection
//     guarantees hold for the mutating path exactly as they do for reads.
//
// The Trash-routed cases redirect $HOME to a sandbox (redirectHomeWithTrash,
// defined in mutate_filesystem_test.go) so the real Trash is never touched.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestSecurityPaths_RemoveRecyclesNeverHardDeletes confirms `remove` stages a
// move INTO the sandboxed Trash rather than any hard-delete command. A staged
// plan that ever used `rm` (or any binary other than `mv`) here would be a
// data-loss regression.
func TestSecurityPaths_RemoveRecyclesNeverHardDeletes(t *testing.T) {
	home := redirectHomeWithTrash(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "doomed.txt")
	writeFileForTest(t, target, "bye")

	plan, err := stageRemove(context.Background(), registry.Capability{}, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("stageRemove: %v", err)
	}
	if plan.Forward.Binary != "mv" {
		t.Errorf("remove Forward uses %q, want mv (a hard delete must never be staged)", plan.Forward.Binary)
	}
	trashDest := plan.Forward.Args[len(plan.Forward.Args)-1]
	if !isInside(trashDest, filepath.Join(home, ".Trash")) {
		t.Errorf("remove recycles to %q, which is not inside the sandbox Trash %q", trashDest, filepath.Join(home, ".Trash"))
	}
}

// TestSecurityPaths_CopyUndoRecyclesNeverHardDeletes confirms the inverse of a
// copy (which must remove the freshly-made duplicate) also recycles to the Trash
// rather than hard-deleting — the same data-loss-avoidance property as remove.
func TestSecurityPaths_CopyUndoRecyclesNeverHardDeletes(t *testing.T) {
	home := redirectHomeWithTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "orig.txt")
	writeFileForTest(t, src, "hello")
	dst := filepath.Join(dir, "dup.txt")

	plan, err := stageCopy(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": dst})
	if err != nil {
		t.Fatalf("stageCopy: %v", err)
	}
	if plan.Inverse == nil {
		t.Fatal("copy should have an inverse")
	}
	if plan.Inverse.Binary != "mv" {
		t.Errorf("copy Inverse uses %q, want mv (undo must recycle, never hard delete)", plan.Inverse.Binary)
	}
	trashDest := plan.Inverse.Args[len(plan.Inverse.Args)-1]
	if !isInside(trashDest, filepath.Join(home, ".Trash")) {
		t.Errorf("copy undo recycles to %q, not inside the sandbox Trash", trashDest)
	}
}

// TestSecurityPaths_MetacharPathStaysLiteralOperand confirms a path full of
// shell metacharacters is staged as one verbatim operand after "--", so it can
// neither be split by a (never-invoked) shell nor parsed as an mkdir flag.
func TestSecurityPaths_MetacharPathStaysLiteralOperand(t *testing.T) {
	dir := t.TempDir()
	weird := filepath.Join(dir, "a; $(reboot) && `rm -rf` b")

	plan, err := stageMkdir(context.Background(), registry.Capability{}, map[string]any{"path": weird})
	if err != nil {
		t.Fatalf("stageMkdir: %v", err)
	}
	args := plan.Forward.Args
	if len(args) < 2 || args[len(args)-2] != "--" {
		t.Fatalf("expected a '--' terminator before the path, got argv %q", args)
	}
	if got := args[len(args)-1]; got != weird {
		t.Errorf("path operand = %q, want it byte-for-byte verbatim (%q)", got, weird)
	}
}

// writeFileForTest writes content to path or fails the test.
func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
}

// isInside reports whether path is lexically within dir.
func isInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

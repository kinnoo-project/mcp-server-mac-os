// mutate_test.go exercises the mutation seam: staging produces the right
// forward/inverse commands and preview, refuses unsafe input, and the
// stage→commit→undo round-trip actually creates and then removes a real
// directory through the engine's RunCommand path.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// mkdirCap is the in-test capability mirroring the manifest's mkdir entry. The
// mutator reads only the normalized "path", so this minimal shape suffices.
var mkdirCap = registry.Capability{
	Name:          "mkdir",
	Category:      "filesystem",
	Binary:        "mkdir",
	Reversibility: registry.Reversible,
	Risk:          registry.RiskMedium,
	Builder:       "mkdir",
	Params: []registry.ParamSpec{
		{Name: "path", Type: registry.TypePath, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
	},
}

// TestStageMkdir_Plan confirms staging a fresh path yields the expected forward
// and inverse commands and a preview, without creating anything.
func TestStageMkdir_Plan(t *testing.T) {
	target := filepath.Join(t.TempDir(), "child")
	eng := New()

	plan, err := eng.Stage(context.Background(), mkdirCap, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if plan.Forward.Binary != "mkdir" || len(plan.Forward.Args) != 2 || plan.Forward.Args[0] != "--" || plan.Forward.Args[1] != target {
		t.Errorf("forward command = %q %v, want mkdir [-- %s]", plan.Forward.Binary, plan.Forward.Args, target)
	}
	if plan.Inverse == nil {
		t.Fatal("mkdir must be reversible (non-nil inverse)")
	}
	if plan.Inverse.Binary != "rmdir" || plan.Inverse.Args[1] != target {
		t.Errorf("inverse command = %q %v, want rmdir [-- %s]", plan.Inverse.Binary, plan.Inverse.Args, target)
	}
	if !strings.Contains(plan.Preview, target) {
		t.Errorf("preview should name the target path: %q", plan.Preview)
	}
	// Staging must not touch the filesystem.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("staging must not create the directory; stat err = %v", err)
	}
}

// TestStageMkdir_RejectsExisting confirms staging refuses a path that already
// exists, so undo can never delete a directory this action did not create.
func TestStageMkdir_RejectsExisting(t *testing.T) {
	existing := t.TempDir() // already exists
	if _, err := New().Stage(context.Background(), mkdirCap, map[string]any{"path": existing}); err == nil {
		t.Fatal("expected Stage to reject an already-existing path")
	}
}

// TestStageMkdir_RejectsDashLeading confirms the flag-injection guard fires on a
// path that begins with '-'.
func TestStageMkdir_RejectsDashLeading(t *testing.T) {
	if _, err := New().Stage(context.Background(), mkdirCap, map[string]any{"path": "-rf"}); err == nil {
		t.Fatal("expected Stage to reject a dash-leading path")
	}
}

// TestMkdir_RoundTrip drives the full reversible flow through the engine: stage,
// run Forward (directory appears), then run Inverse (directory is gone).
func TestMkdir_RoundTrip(t *testing.T) {
	target := filepath.Join(t.TempDir(), "made")
	eng := New()
	ctx := context.Background()

	plan, err := eng.Stage(ctx, mkdirCap, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	if _, err := eng.RunCommand(ctx, plan.Forward); err != nil {
		t.Fatalf("RunCommand(forward): %v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("forward command should have created the directory; stat err = %v", err)
	}

	if _, err := eng.RunCommand(ctx, *plan.Inverse); err != nil {
		t.Fatalf("RunCommand(inverse): %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("inverse command should have removed the directory; stat err = %v", err)
	}
}

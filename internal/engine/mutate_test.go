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

// TestRunCommand_StdinIsPureData proves the Command.Stdin channel delivers a
// payload to the child verbatim: content full of flag-like and metacharacter
// text (`-e`, `--`, `$(...)`) comes back byte-for-byte from cat, demonstrating
// stdin bytes are never parsed as options, paths, or shell. This is the
// injection-safety property that lets write_file/write_clipboard-style
// mutators carry model-controlled content without argv hardening.
func TestRunCommand_StdinIsPureData(t *testing.T) {
	hostile := "-e beep\n--flag-looking line\n$(rm -rf ~) `id` ; echo pwned\n"
	out, err := New().RunCommand(context.Background(), Command{
		Binary: "cat",
		Stdin:  []byte(hostile),
	})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(out, hostile) {
		t.Errorf("stdin payload must round-trip verbatim as data; got %q", out)
	}
}

// TestRunCommand_NilStdinUnchanged pins the zero-value behavior: a Command
// without a payload runs exactly as before the Stdin field existed.
func TestRunCommand_NilStdinUnchanged(t *testing.T) {
	out, err := New().RunCommand(context.Background(), Command{Binary: "echo", Args: []string{"ok"}})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected plain argv output, got %q", out)
	}
}

// TestPreviewSnippet covers the preview-rendering contract: byte counts are
// exact, only the first line is shown, long or multi-line payloads are marked
// as elided, and control characters arrive escaped rather than raw.
func TestPreviewSnippet(t *testing.T) {
	long := strings.Repeat("a", 100)
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", "", "empty (0 bytes)"},
		{"single_line", "hello", `5 bytes: "hello"`},
		{"multi_line_elided", "first\nsecond", `12 bytes, beginning "first" …`},
		{"long_line_truncated", long, `100 bytes, beginning "` + strings.Repeat("a", 80) + `" …`},
		{"crlf_trimmed", "top\r\nrest", `9 bytes, beginning "top" …`},
		{"control_chars_escaped", "bad\x1b[31m", `8 bytes: "bad\x1b[31m"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewSnippet([]byte(tc.content)); got != tc.want {
				t.Errorf("previewSnippet(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
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

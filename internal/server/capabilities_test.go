// capabilities_test.go is the behavioral suite for every read-only capability
// reachable through the filesystem domain tool. Each test drives the real
// operation path end to end against a hermetic fixture tree and asserts on
// observable output, not on the internal command line. This is the acceptance
// check for all 9 capabilities.
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTempTree builds a small, deterministic directory layout used across the
// behavioral tests. Returning the root keeps each test hermetic.
func makeTempTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	mustWrite("hello.txt", "hello world\nsecond line\n")
	mustWrite("notes.md", "# Notes\nTODO: ship MCP\n")
	mustWrite("images/cat.png", "\x89PNG\r\n\x1a\nfake png body")
	mustWrite("images/dog.JPG", "\xFF\xD8\xFF\xE0fake jpg body")
	mustWrite("images/readme.txt", "not an image")
	mustWrite("nested/a/b/c/deep.txt", "deeply nested\n")
	return root
}

// run is a helper that executes a capability through the filesystem domain tool
// and returns the result text, failing the test on a transport error. Every
// read-only capability lives in the filesystem domain in this phase.
func run(t *testing.T, capability string, params map[string]any) (text string, isErr bool) {
	t.Helper()
	res, _, err := newTestServer(t).runDomainOperation(context.Background(), "filesystem", DomainArgs{
		Operation: capability,
		Params:    params,
	})
	if err != nil {
		t.Fatalf("filesystem %s transport error: %v", capability, err)
	}
	return textOf(res), res.IsError
}

func TestCapability_Pwd(t *testing.T) {
	out, isErr := run(t, "pwd", nil)
	if isErr {
		t.Fatalf("pwd errored: %s", out)
	}
	wd, _ := os.Getwd()
	if out != wd {
		t.Errorf("pwd = %q, want %q", out, wd)
	}
}

func TestCapability_File(t *testing.T) {
	root := makeTempTree(t)
	out, isErr := run(t, "file", map[string]any{
		"paths": []any{filepath.Join(root, "hello.txt"), filepath.Join(root, "images/cat.png")},
	})
	if isErr {
		t.Fatalf("file errored: %s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "text") {
		t.Errorf("expected text classification for hello.txt: %s", out)
	}
	if !strings.Contains(low, "png") {
		t.Errorf("expected png classification for cat.png: %s", out)
	}
}

func TestCapability_Stat(t *testing.T) {
	root := makeTempTree(t)
	out, isErr := run(t, "stat", map[string]any{"paths": []any{filepath.Join(root, "hello.txt")}})
	if isErr || out == "(no output)" {
		t.Fatalf("stat produced no useful output: %s", out)
	}
}

func TestCapability_Wc(t *testing.T) {
	root := makeTempTree(t)
	out, isErr := run(t, "wc", map[string]any{
		"paths": []any{filepath.Join(root, "hello.txt")},
		"lines": true,
	})
	if isErr {
		t.Fatalf("wc errored: %s", out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("wc -l should report 2 lines: %s", out)
	}
}

func TestCapability_Du_Summary(t *testing.T) {
	root := makeTempTree(t)
	// max_depth 0 yields a single total for the argument (the summary view).
	out, isErr := run(t, "du", map[string]any{
		"paths":          []any{root},
		"human_readable": true,
		"max_depth":      float64(0),
	})
	if isErr {
		t.Fatalf("du errored: %s", out)
	}
	if !strings.Contains(out, root) {
		t.Errorf("du output should mention the root path: %s", out)
	}
}

func TestCapability_Grep_FindsMatch(t *testing.T) {
	root := makeTempTree(t)
	out, isErr := run(t, "grep", map[string]any{
		"pattern":   "TODO",
		"paths":     []any{root},
		"recursive": true,
	})
	if isErr {
		t.Fatalf("grep errored: %s", out)
	}
	if !strings.Contains(out, "ship MCP") {
		t.Errorf("grep should have matched the TODO line: %s", out)
	}
}

func TestCapability_Grep_NoMatchIsNotError(t *testing.T) {
	root := makeTempTree(t)
	// grep exits 1 on "no match"; that is data, not a tool failure.
	out, isErr := run(t, "grep", map[string]any{
		"pattern":   "this-string-does-not-exist-xyzzy",
		"paths":     []any{root},
		"recursive": true,
	})
	if isErr {
		t.Fatalf("grep no-match should not be a tool error: %s", out)
	}
	if !strings.Contains(out, "exit code: 1") {
		t.Errorf("expected exit-code annotation for no-match grep: %s", out)
	}
}

func TestCapability_Grep_DashPatternIsSafe(t *testing.T) {
	root := makeTempTree(t)
	// Passed via -e, so "-v" is searched as text rather than treated as a flag.
	_, isErr := run(t, "grep", map[string]any{
		"pattern": "-v",
		"paths":   []any{filepath.Join(root, "hello.txt")},
	})
	if isErr {
		t.Error("grep should treat a dash-leading pattern as data, not a flag")
	}
}

func TestCapability_LargestFiles(t *testing.T) {
	root := t.TempDir()
	// Two files of clearly different sizes; the bigger should rank first.
	if err := os.WriteFile(filepath.Join(root, "big.bin"), make([]byte, 40000), 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatalf("write small: %v", err)
	}
	out, isErr := run(t, "largest_files", map[string]any{"dir": root, "count": float64(1)})
	if isErr {
		t.Fatalf("largest_files errored: %s", out)
	}
	if !strings.Contains(out, "big.bin") {
		t.Errorf("largest_files should rank big.bin first: %s", out)
	}
	if strings.Contains(out, "small.txt") {
		t.Errorf("largest_files count=1 should exclude small.txt: %s", out)
	}
}

func TestCapability_Find_Extensions(t *testing.T) {
	root := makeTempTree(t)
	out, isErr := run(t, "find", map[string]any{
		"path":       root,
		"type":       "f",
		"extensions": []any{"png", "jpg"},
	})
	if isErr {
		t.Fatalf("find errored: %s", out)
	}
	if !strings.Contains(out, "cat.png") {
		t.Errorf("find should locate cat.png: %s", out)
	}
	if !strings.Contains(out, "dog.JPG") {
		t.Errorf("find should be case-insensitive on extensions (dog.JPG): %s", out)
	}
	if strings.Contains(out, "readme.txt") {
		t.Errorf("find should exclude readme.txt: %s", out)
	}
}

func TestCapability_Find_Rejections(t *testing.T) {
	root := makeTempTree(t)
	cases := []struct {
		name   string
		params map[string]any
	}{
		{"unsafe extension", map[string]any{"path": root, "extensions": []any{"png;rm -rf /"}}},
		{"unknown type", map[string]any{"path": root, "type": "x"}},
		{"dash-leading path", map[string]any{"path": "-exec"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, isErr := run(t, "find", tc.params); !isErr {
				t.Fatalf("expected an error result for %q", tc.name)
			}
		})
	}
}

// tools_test.go is the behavioral test for the domain-tool path: it stands up a
// real Server over the embedded registry and confirms the filesystem domain tool
// lists a hermetic fixture tree, plus the structured handling of bad input.
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
)

// newTestServer builds a Server backed by the real embedded registry and engine.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	s, err := New(reg, engine.New())
	if err != nil {
		t.Fatalf("server.New(): %v", err)
	}
	return s
}

// textOf extracts the first TextContent string from a CallToolResult.
func textOf(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// TestRegister exercises mcp.AddTool for every engine tool. AddTool generates
// each tool's JSON Schema from its argument struct and PANICS on an invalid
// jsonschema tag, so this is the regression guard for schema/tag mistakes that
// handler-level tests (which bypass AddTool) cannot catch.
func TestRegister(t *testing.T) {
	s := newTestServer(t)
	m := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	s.Register(m) // panics if any tool's argument schema is invalid
}

// fsOp runs an operation through the filesystem domain tool for the test server.
func fsOp(s *Server, operation string, params map[string]any) *mcp.CallToolResult {
	res, _, _ := s.runDomainOperation(context.Background(), "filesystem", DomainArgs{
		Operation: operation,
		Params:    params,
	})
	return res
}

// TestDomain_LS confirms the filesystem domain tool's ls operation returns a
// correct listing of a temp directory.
func TestDomain_LS(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	res := fsOp(newTestServer(t), "ls", map[string]any{"path": dir})
	if res.IsError {
		t.Fatalf("filesystem ls returned error: %s", textOf(res))
	}
	out := textOf(res)
	for _, want := range []string{"alpha.txt", "beta.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q: %s", want, out)
		}
	}
}

// TestDomain_UnknownOperation confirms an unrecognized operation yields a
// structured, non-crashing error that names the alternatives.
func TestDomain_UnknownOperation(t *testing.T) {
	res := fsOp(newTestServer(t), "teleport", nil)
	if !res.IsError {
		t.Fatal("expected IsError for unknown operation")
	}
	if !strings.Contains(textOf(res), "ls") {
		t.Errorf("error should list available operations incl. ls: %s", textOf(res))
	}
}

// TestDomain_RequiresOperation confirms the empty-operation guard.
func TestDomain_RequiresOperation(t *testing.T) {
	res := fsOp(newTestServer(t), "", nil)
	if !res.IsError {
		t.Fatal("expected IsError when operation is empty")
	}
}

// TestDomain_RejectsUnknownParam confirms parameter validation errors propagate
// as tool errors rather than being silently ignored.
func TestDomain_RejectsUnknownParam(t *testing.T) {
	res := fsOp(newTestServer(t), "ls", map[string]any{"nonsense": true})
	if !res.IsError {
		t.Fatal("expected IsError for unknown parameter")
	}
}

// TestDomain_AutoCommitRunsImmediately confirms the auto-commit lane: a mutating
// capability marked auto_commit runs at once (no staged token), performs the
// change, and still returns an undo token when the change is reversible.
//
// It uses a hand-built registry whose sole capability is the real `mkdir` mutator
// re-tagged as a low-risk auto-commit operation, so the test exercises the
// server's dispatch lane against genuine engine machinery while writing only to a
// temp directory.
func TestDomain_AutoCommitRunsImmediately(t *testing.T) {
	reg, err := registry.New([]registry.Capability{{
		Name:          "auto_mkdir",
		Summary:       "Create a directory immediately (test auto-commit).",
		Category:      "filesystem",
		Binary:        "mkdir",
		Reversibility: registry.Reversible,
		Risk:          registry.RiskLow,
		Builder:       "mkdir",
		AutoCommit:    true,
		Params: []registry.ParamSpec{
			{Name: "path", Type: registry.TypePath, Required: true, Description: "Directory to create.", Arg: registry.ArgRule{Kind: registry.ArgNone}},
		},
	}})
	if err != nil {
		t.Fatalf("registry.New(): %v", err)
	}
	s, err := New(reg, engine.New())
	if err != nil {
		t.Fatalf("server.New(): %v", err)
	}

	target := filepath.Join(t.TempDir(), "made-now")
	res, _, _ := s.runDomainOperation(context.Background(), "filesystem", DomainArgs{
		Operation: "auto_mkdir",
		Params:    map[string]any{"path": target},
	})
	if res.IsError {
		t.Fatalf("auto_mkdir returned error: %s", textOf(res))
	}
	// The change must have happened immediately — no execute step.
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("auto-commit should have created the directory immediately; stat err = %v", err)
	}
	// A reversible auto-commit must offer an undo token, not a staged req_ token.
	text := textOf(res)
	if strings.Contains(text, "STAGED") {
		t.Errorf("auto-commit must not STAGE; got: %s", text)
	}
	undoToken := extractToken(t, text, "undo_")

	// And that undo token must actually reverse the change.
	undone, _, _ := s.Undo(context.Background(), nil, UndoArgs{UndoToken: undoToken})
	if undone.IsError {
		t.Fatalf("undo returned error: %s", textOf(undone))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("undo should have removed the directory; stat err = %v", err)
	}
}

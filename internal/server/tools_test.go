// tools_test.go is the behavioral test for the query path: it stands up a real
// Server over the embedded registry and confirms query(ls) lists a hermetic
// fixture tree, plus the structured handling of bad input.
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

// TestQuery_LS is the Slice B acceptance check: query(ls) returns a correct
// listing of a temp directory.
func TestQuery_LS(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	s := newTestServer(t)
	res, _, err := s.Query(context.Background(), nil, QueryArgs{
		Capability: "ls",
		Params:     map[string]any{"path": dir},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.IsError {
		t.Fatalf("query ls returned error: %s", textOf(res))
	}
	out := textOf(res)
	for _, want := range []string{"alpha.txt", "beta.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q: %s", want, out)
		}
	}
}

// TestQuery_UnknownCapability confirms an unrecognized capability yields a
// structured, non-crashing error that names the alternatives.
func TestQuery_UnknownCapability(t *testing.T) {
	s := newTestServer(t)
	res, _, err := s.Query(context.Background(), nil, QueryArgs{Capability: "teleport"})
	if err != nil {
		t.Fatalf("Query transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for unknown capability")
	}
	if !strings.Contains(textOf(res), "ls") {
		t.Errorf("not_found should list available capabilities incl. ls: %s", textOf(res))
	}
}

// TestQuery_RequiresCapability confirms the empty-capability guard.
func TestQuery_RequiresCapability(t *testing.T) {
	s := newTestServer(t)
	res, _, _ := s.Query(context.Background(), nil, QueryArgs{})
	if !res.IsError {
		t.Fatal("expected IsError when capability is empty")
	}
}

// TestQuery_RejectsUnknownParam confirms validation errors propagate as tool
// errors rather than being silently ignored.
func TestQuery_RejectsUnknownParam(t *testing.T) {
	s := newTestServer(t)
	res, _, _ := s.Query(context.Background(), nil, QueryArgs{
		Capability: "ls",
		Params:     map[string]any{"nonsense": true},
	})
	if !res.IsError {
		t.Fatal("expected IsError for unknown parameter")
	}
}

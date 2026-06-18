// integration_test.go exercises the server the way a real MCP client does:
// a client and our server are connected over the SDK's in-memory transport, and
// we drive the actual protocol (list tools, call tools). This verifies the whole
// stack end to end — registration, schema generation, dispatch, and result
// encoding — through the same machinery a production client uses, without the
// flakiness of hand-framing JSON-RPC over a pipe.
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

// connectClient wires our registered server to a client over in-memory
// transports and returns the connected client session.
func connectClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	s, err := New(reg, engine.New())
	if err != nil {
		t.Fatalf("server.New(): %v", err)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mac-os-mcp-server", Version: "test"}, nil)
	s.Register(mcpServer)

	clientT, serverT := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := mcpServer.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestIntegration_ToolSurface confirms the protocol exposes one domain tool per
// category — currently the single `filesystem` tool — alongside the two fixed
// mutation-lifecycle tools (`execute`, `undo`), and that the domain tool's
// description embeds the full operation menu so the model needs no separate
// discovery call.
func TestIntegration_ToolSurface(t *testing.T) {
	cs := connectClient(t)
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range lt.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"filesystem", "execute", "undo"} {
		if !got[want] {
			t.Errorf("expected tool %q in surface, got %v", want, toolNames(lt))
		}
	}
	if len(lt.Tools) != 3 {
		t.Errorf("expected exactly 3 tools (filesystem, execute, undo), got %v", toolNames(lt))
	}

	// Every operation — read-only and mutating — should appear in the menu.
	var desc string
	for _, tool := range lt.Tools {
		if tool.Name == "filesystem" {
			desc = tool.Description
		}
	}
	for _, op := range []string{"ls", "pwd", "file", "stat", "wc", "du", "find", "grep", "largest_files", "mkdir"} {
		if !strings.Contains(desc, op) {
			t.Errorf("filesystem tool description missing operation %q", op)
		}
	}
}

// TestIntegration_MutationLifecycle drives the full stage → execute → undo flow
// over the real protocol, the way an MCP client would: staging mkdir returns a
// token and creates nothing; execute(token) creates the directory and returns an
// undo_token; undo(undo_token) removes it again.
func TestIntegration_MutationLifecycle(t *testing.T) {
	cs := connectClient(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "made-by-mcp")

	// Stage: should hand back a token and leave the filesystem untouched.
	staged, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "filesystem",
		Arguments: map[string]any{"operation": "mkdir", "params": map[string]any{"path": target}},
	})
	if err != nil {
		t.Fatalf("CallTool stage mkdir: %v", err)
	}
	if staged.IsError {
		t.Fatalf("stage mkdir returned error: %s", textOf(staged))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("staging must not create the directory; stat err = %v", err)
	}
	token := extractToken(t, textOf(staged), "req_")

	// Execute: should create the directory and return an undo_token.
	executed, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "execute",
		Arguments: map[string]any{"token": token},
	})
	if err != nil {
		t.Fatalf("CallTool execute: %v", err)
	}
	if executed.IsError {
		t.Fatalf("execute returned error: %s", textOf(executed))
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("execute should have created the directory; stat err = %v", err)
	}
	undoToken := extractToken(t, textOf(executed), "undo_")

	// Undo: should remove the directory again.
	undone, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "undo",
		Arguments: map[string]any{"undo_token": undoToken},
	})
	if err != nil {
		t.Fatalf("CallTool undo: %v", err)
	}
	if undone.IsError {
		t.Fatalf("undo returned error: %s", textOf(undone))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("undo should have removed the directory; stat err = %v", err)
	}
}

// extractToken pulls the first QUOTED token carrying the given prefix out of a
// tool's text result, failing the test if none is present. Anchoring on the
// opening quote (e.g. `"req_abc123"`) avoids matching the prefix where it appears
// as plain prose — notably the label "undo_token" in the execute message — and
// mirrors how a model would read the token back out of the response.
func extractToken(t *testing.T, text, prefix string) string {
	t.Helper()
	marker := `"` + prefix
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("expected a quoted %s token in result, got: %s", prefix, text)
	}
	tok := text[idx+1:] // skip the opening quote
	end := strings.IndexByte(tok, '"')
	if end < 0 {
		t.Fatalf("expected a closing quote after %s token, got: %s", prefix, text)
	}
	return tok[:end]
}

// TestIntegration_FilesystemPwd calls the pwd operation through the filesystem
// domain tool over the protocol and confirms a real working-directory string
// comes back.
func TestIntegration_FilesystemPwd(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "filesystem",
		Arguments: map[string]any{"operation": "pwd"},
	})
	if err != nil {
		t.Fatalf("CallTool filesystem pwd: %v", err)
	}
	if res.IsError {
		t.Fatalf("filesystem pwd returned error: %s", textOf(res))
	}
	if !strings.HasPrefix(textOf(res), "/") {
		t.Errorf("pwd should return an absolute path, got %q", textOf(res))
	}
}

func toolNames(lt *mcp.ListToolsResult) []string {
	names := make([]string, 0, len(lt.Tools))
	for _, tool := range lt.Tools {
		names = append(names, tool.Name)
	}
	return names
}

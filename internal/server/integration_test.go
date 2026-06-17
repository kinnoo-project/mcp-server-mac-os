// integration_test.go exercises the server the way a real MCP client does:
// a client and our server are connected over the SDK's in-memory transport, and
// we drive the actual protocol (list tools, call tools). This verifies the whole
// stack end to end — registration, schema generation, dispatch, and result
// encoding — through the same machinery a production client uses, without the
// flakiness of hand-framing JSON-RPC over a pipe.
package server

import (
	"context"
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

// TestIntegration_ToolSurface confirms the protocol exposes exactly one domain
// tool per category — currently the single `filesystem` tool — and that its
// description embeds the full operation menu so the model needs no separate
// discovery call.
func TestIntegration_ToolSurface(t *testing.T) {
	cs := connectClient(t)
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(lt.Tools) != 1 || lt.Tools[0].Name != "filesystem" {
		t.Fatalf("expected exactly one tool 'filesystem', got %v", toolNames(lt))
	}
	// Every read-only operation should appear in the embedded menu.
	desc := lt.Tools[0].Description
	for _, op := range []string{"ls", "pwd", "file", "stat", "wc", "du", "find", "grep"} {
		if !strings.Contains(desc, op) {
			t.Errorf("filesystem tool description missing operation %q", op)
		}
	}
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

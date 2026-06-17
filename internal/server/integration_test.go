// integration_test.go exercises the server the way a real MCP client does:
// a client and our server are connected over the SDK's in-memory transport, and
// we drive the actual protocol (list tools, call tools). This verifies the whole
// stack end to end — registration, schema generation, dispatch, and result
// encoding — through the same machinery a production client uses, without the
// flakiness of hand-framing JSON-RPC over a pipe.
package server

import (
	"context"
	"encoding/json"
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

// TestIntegration_ToolSurface confirms the protocol exposes exactly the three
// fixed engine tools (Pattern A), regardless of capability count.
func TestIntegration_ToolSurface(t *testing.T) {
	cs := connectClient(t)
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool)
	for _, tool := range lt.Tools {
		got[tool.Name] = true
	}
	want := []string{"query", "list_capabilities", "describe_capability"}
	if len(lt.Tools) != len(want) {
		t.Errorf("exposed %d tools, want %d (%v)", len(lt.Tools), len(want), toolNames(lt))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing expected tool %q; have %v", name, toolNames(lt))
		}
	}
}

// TestIntegration_QueryPwd calls the pwd capability over the protocol and
// confirms a real working-directory string comes back.
func TestIntegration_QueryPwd(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"capability": "pwd"},
	})
	if err != nil {
		t.Fatalf("CallTool query pwd: %v", err)
	}
	if res.IsError {
		t.Fatalf("query pwd returned error: %s", textOf(res))
	}
	if !strings.HasPrefix(textOf(res), "/") {
		t.Errorf("pwd should return an absolute path, got %q", textOf(res))
	}
}

// TestIntegration_ListCapabilities calls list_capabilities over the protocol and
// confirms every registered capability is reported.
func TestIntegration_ListCapabilities(t *testing.T) {
	cs := connectClient(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_capabilities",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool list_capabilities: %v", err)
	}
	var caps []capabilitySummary
	if err := json.Unmarshal([]byte(textOf(res)), &caps); err != nil {
		t.Fatalf("list_capabilities output is not valid JSON: %v", err)
	}
	if len(caps) != 8 {
		t.Errorf("listed %d capabilities, want 8", len(caps))
	}
}

func toolNames(lt *mcp.ListToolsResult) []string {
	names := make([]string, 0, len(lt.Tools))
	for _, tool := range lt.Tools {
		names = append(names, tool.Name)
	}
	return names
}

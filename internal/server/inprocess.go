// inprocess.go wires a real Server (real registry + real engine) to an MCP
// client over the SDK's in-memory transport, with no subprocess and no stdio
// framing involved.
//
// This is the same shape a production client/server pair takes — the only
// difference is the transport — so anything driven through the returned client
// session exercises the actual tool dispatch, parameter validation, and
// execution code paths. Two callers need exactly this: the integration test
// suite (asserting protocol-level behavior) and the eval harness
// (internal/evals, letting a real model choose and invoke tool calls against
// the real server rather than a stub).
package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
)

// Connect loads the embedded registry, builds a Server over it, registers the
// server's tools onto a fresh in-memory MCP transport, and returns a connected
// client session. The returned cleanup func closes both the client and server
// sessions and should be deferred by the caller.
func Connect(ctx context.Context) (*mcp.ClientSession, func(), error) {
	reg, err := registry.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("registry.Load(): %w", err)
	}
	s, err := New(reg, engine.New())
	if err != nil {
		return nil, nil, fmt.Errorf("server.New(): %w", err)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mac-os-mcp-server", Version: "in-process"}, nil)
	s.Register(mcpServer)

	clientT, serverT := mcp.NewInMemoryTransports()

	ss, err := mcpServer.Connect(ctx, serverT, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("server.Connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "in-process-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = ss.Close()
		return nil, nil, fmt.Errorf("client.Connect: %w", err)
	}

	cleanup := func() {
		_ = cs.Close()
		_ = ss.Close()
	}
	return cs, cleanup, nil
}

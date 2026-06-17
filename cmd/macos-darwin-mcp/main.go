// Command macos-darwin-mcp is the entry point for the macOS capability MCP
// server. It performs only wiring; all behavior lives in the internal packages.
//
// Boot sequence:
//  1. Force logging to stderr so the JSON-RPC stream on stdout stays clean (a
//     core protocol invariant — any stray stdout write corrupts framing).
//  2. Load and validate the capability registry (fails fast on a bad manifest).
//  3. Build the engine and server, which cross-checks that every capability's
//     builder actually exists (fails fast on a misconfigured capability).
//  4. Register one domain tool per capability category and serve over stdio
//     until the client disconnects.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-server-mac-os/internal/engine"
	"mcp-server-mac-os/internal/registry"
	"mcp-server-mac-os/internal/server"
)

func main() {
	log.SetOutput(os.Stderr)

	reg, err := registry.Load()
	if err != nil {
		log.Fatalf("failed to load capability registry: %v", err)
	}

	srv, err := server.New(reg, engine.New())
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}

	impl := &mcp.Implementation{
		Name:    "mac-os-mcp-server",
		Version: "0.4.0",
	}
	mcpServer := mcp.NewServer(impl, nil)
	srv.Register(mcpServer)

	log.Printf("Starting mac-os-mcp-server (%d read-only capabilities across %d domain(s)) over stdio...", reg.Len(), len(srv.Domains()))
	if err := mcpServer.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}

package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// main boots the read-only macOS filesystem MCP server over stdio.
//
// Engineering invariants enforced here (see CLAUDE.md):
//   - log output is forced to stderr so the JSON-RPC framing on stdout stays
//     clean.
//   - every tool is wired through registerTools, which only registers
//     informational/navigational utilities — no mutating commands are exposed.
func main() {
	log.SetOutput(os.Stderr)

	impl := &mcp.Implementation{
		Name:    "mac-os-mcp-server",
		Version: "0.2.0",
	}

	server := mcp.NewServer(impl, nil)
	registerTools(server)

	log.Println("Starting mac-os-mcp-server (read-only filesystem tools) over stdio...")
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server exited with error: %v", err)
	}
}

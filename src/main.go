package main

import (
  "context"
  "fmt"
  "log"
  "os"

  "github.com/modelcontextprotocol/go-sdk/mcp"
)

type EchoArgs struct {
  Text string `json:"text" jsonschema:"required,description=The text to echo back."`
}

type EmptyResult struct{}

func main() {
  log.SetOutput(os.Stderr)

  impl := &mcp.Implementation{
    Name:    "mac-os-mcp-server",
    Version: "0.1.0",
  }

  server := mcp.NewServer(impl, nil)
  if err := mcp.AddTool(server, &mcp.Tool{
    Name:        "echo",
    Description: "Echo back input text.",
  }, echoHandler); err != nil {
    log.Fatalf("Failed to register tool: %v", err)
  }

  log.Println("Starting mac-os-mcp-server over stdio...")
  if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
    log.Fatalf("Server exited with error: %v", err)
  }
}

func echoHandler(_ context.Context, _ *mcp.CallToolRequest, args EchoArgs) (*mcp.CallToolResult, EmptyResult, error) {
  result := &mcp.CallToolResult{
    Content: []mcp.Content{
      &mcp.TextContent{Text: fmt.Sprintf("echo: %s", args.Text)},
    },
  }
  return result, EmptyResult{}, nil
}

---
name: go-mcp-sdk-conventions
description: Enforces type safety, JSON schema structures, and official Go MCP SDK v1.6.0 patterns
applyTo: ".*\\.go"
---

# Go Programming & MCP SDK Standards

## 1. Automated Schema Contracts via Struct Tags
Every tool registered on the server must accept an explicit, strongly typed arguments struct. Coding agents must utilize precise `jsonschema` tags to document metadata dependencies for the LLM client.

```go
type CommandArguments struct {
	BinaryPath string   `json:"binary_path" jsonschema:"required,description=The absolute system path to the target tool execution vector"`
	Parameters []string `json:"parameters" jsonschema:"description=Pre-tokenized slice of argument flags and target strings"`
}

```

## 2. Mandatory Handler Contract

Every handler mapped into `mcp.AddTool` must adhere strictly to the typed generic signature model expected by the v1.6.0 SDK infrastructure layer:

```go
func ToolHandlerName(ctx context.Context, req *mcp.CallToolRequest, args YourArgumentsStruct) (*mcp.CallToolResult, YourCustomMetadataOutputStruct, error) {
	// Logic layout rule:
	// 1. Immediately verify incoming context cancellation bounds.
	// 2. Perform local constraint checking or bounds validation.
	// 3. Return tool execution results or explicit protocol error codes.
}

```

## 3. Graceful Context Cascading

Never instantiate empty execution loops or unmonitored tracking blocks. Always tie background processing pipelines directly to the incoming request lifecycle:

```go
// Enforces process termination if the AI agent or user drops the socket loop
cmd := exec.CommandContext(ctx, args.BinaryPath, args.Parameters...)

```

## 4. Signal Safety on Stdout Tracking

To enforce the first rule of the project macro structure, ensure the `main()` function explicitly forces logging configurations to route downstream to the standard error console immediately upon boot:

```go
log.SetOutput(os.Stderr)

```

---

### 🧠 How Claude Code Processes This File

You don't need to manually import or reference this file anywhere else in your project. Because of the top YAML block configuration:

```yaml
applyTo: ".*\\.go"

```

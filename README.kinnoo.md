# mcp-server-mac-os

This scaffold demonstrates a minimal Go MCP server over stdio using the official MCP Go SDK.

Kinnoo scaffolding pins the SDK dependency to `github.com/modelcontextprotocol/go-sdk v1.4.1`.

## Run Server
```
go run main.go
```

## Notes
- If you make manual dependency changes, run `go mod tidy`.
- If the MCP Go SDK introduces breaking changes, the Kinnoo CLI Go MCP templates may need updates.

## Quick Handshake Smoke Test (from another terminal)
```
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
' | go run main.go
```

- Edit `src/main.go` to implement your agent run logic.
- `kinnoo.yaml` holds the agent manifest and runtime metadata contract.

## Folder Guide

| Folder | What goes here |
| --- | --- |
| tools/ | Tool wrappers and utility code the agent can call |
| prompts/ | Prompt snippets and reusable instructions |
| evals/ | Evaluation cases and scoring fixtures |
| tests/ | Regression and smoke tests for this agent |
| data/ | Local sample data and offline test fixtures |

---
🍊 *This agent was scaffolded with Kinnoo CLI v0.10.2 using Schema 0.1.0.*
